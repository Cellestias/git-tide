package history

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/cellestias/git-tide/internal/apperr"
	domain "github.com/cellestias/git-tide/internal/domain/config"
	"github.com/cellestias/git-tide/internal/git/repository"
	"github.com/cellestias/git-tide/internal/output"
)

// MaxCompareCommits bounds the commits simulated: each one costs a merge.
const MaxCompareCommits = 300

// Pick statuses.
const (
	StatusClean    = "clean"    // cherry-picks without conflicts
	StatusConflict = "conflict" // does not cherry-pick cleanly
	StatusApplied  = "applied"  // an equivalent patch is already in the target
	StatusEmpty    = "empty"    // would change nothing on the target
	StatusError    = "error"    // could not be simulated (for example a root commit)
)

// pick is one commit of the first branch and how it would land on the second.
type pick struct {
	entry         repository.LogEntry
	status        string
	conflicts     []string // files, for StatusConflict
	skippedBefore []string // conflicting commits left out when this one was tried
}

// Compare reports how far `from` is ahead of and behind `to`, and whether each
// commit `from` has would cherry-pick cleanly onto `to`. The commits are tried
// oldest first, each on top of the ones that applied, so a commit that depends
// on an earlier one is judged fairly. Nothing in the repository is changed.
// A conflicting commit is left out of the simulation, and the commits after it
// are tried without it.
func Compare(ctx context.Context, repo *repository.Repository, cfg domain.Config, from, to string, opts Options, p output.Printer) error {
	if bad := incompatible(opts); len(bad) > 0 {
		return apperr.Usage("--compare cannot be combined with %s", strings.Join(bad, ", "))
	}
	if opts.Limit < 0 {
		return apperr.Usage("--limit must not be negative")
	}
	if err := checkGit(ctx, repo); err != nil {
		return err
	}
	fromName, err := branchName(ctx, repo, from)
	if err != nil {
		return err
	}
	toName, err := branchName(ctx, repo, to)
	if err != nil {
		return err
	}
	fromRev, fromLabel, err := resolveRev(ctx, repo, cfg.Remote, fromName, opts.Remote, p)
	if err != nil {
		return err
	}
	toRev, toLabel, err := resolveRev(ctx, repo, cfg.Remote, toName, opts.Remote, p)
	if err != nil {
		return err
	}

	ahead, err := repo.Log(ctx, repository.LogQuery{Rev: fromRev, Exclude: toRev})
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot read the commit history", "", err)
	}
	if len(ahead) > MaxCompareCommits {
		return apperr.New(apperr.CodeUsage, fmt.Sprintf("%s is %d commits ahead of %s; --compare handles up to %d", fromLabel, len(ahead), toLabel, MaxCompareCommits),
			"compare against a branch closer to it")
	}
	behind, err := repo.Log(ctx, repository.LogQuery{Rev: toRev, Exclude: fromRev, Limit: opts.Limit})
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot read the commit history", "", err)
	}
	slices.Reverse(ahead) // oldest first: the order they would be applied
	slices.Reverse(behind)

	equivalent, err := repo.Cherry(ctx, toRev, fromRev)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot compare the patches", "", err)
	}
	picks, err := simulate(ctx, repo, toRev, ahead, equivalent)
	if err != nil {
		return err
	}

	base, err := repo.MergeBase(ctx, fromRev, toRev)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot find the merge base", "", err)
	}
	var overlap []string
	if base != "" {
		if overlap, err = overlapping(ctx, repo, base, fromRev, toRev); err != nil {
			return apperr.Wrap(apperr.CodeExternal, "cannot compare the changed files", "", err)
		}
	}

	conflicts := 0
	for _, pk := range picks {
		if pk.status == StatusConflict || pk.status == StatusError {
			conflicts++
		}
	}
	var failure error
	if conflicts > 0 {
		failure = apperr.New(apperr.CodeUnsafeRepo,
			fmt.Sprintf("%d of %d commit%s cherry-pick cleanly onto %s, %d conflict", len(picks)-conflicts-settled(picks), len(picks), plural(len(picks)), toLabel, conflicts), "")
	}

	if opts.JSON {
		if err := writeCompareJSON(p, fromLabel, toLabel, base, picks, behind, overlap); err != nil {
			return err
		}
		return failure
	}
	renderCompare(p, fromLabel, toLabel, base, picks, behind, overlap)
	return failure
}

// incompatible names the list-only flags that mean nothing to a comparison.
func incompatible(o Options) []string {
	var bad []string
	for _, f := range []struct {
		name string
		set  bool
	}{
		{"--me", o.Me}, {"--member", o.Member != ""}, {"--branch", o.Branch != ""}, {"--file", o.File != ""},
		{"--all", o.All}, {"--base", o.Base != ""}, {"--merges", o.Merges},
		{"--since", o.Since != ""}, {"--until", o.Until != ""},
	} {
		if f.set {
			bad = append(bad, f.name)
		}
	}
	return bad
}

// checkGit fails clearly on a Git too old for the simulation.
func checkGit(ctx context.Context, repo *repository.Repository) error {
	major, minor, err := repo.GitVersion(ctx)
	if err != nil {
		return apperr.Wrap(apperr.CodeExternal, "cannot read the Git version", "", err)
	}
	if major < repository.MinCompareMajor || (major == repository.MinCompareMajor && minor < repository.MinCompareMinor) {
		return apperr.New(apperr.CodeExternal,
			fmt.Sprintf("--compare needs Git %d.%d or later; this is Git %d.%d", repository.MinCompareMajor, repository.MinCompareMinor, major, minor),
			"update Git (https://git-scm.com/downloads)")
	}
	return nil
}

// simulate tries each commit, oldest first, on top of those that applied.
func simulate(ctx context.Context, repo *repository.Repository, target string, commits []repository.LogEntry, equivalent map[string]bool) ([]pick, error) {
	onto := target
	ontoTree, err := repo.Tree(ctx, onto)
	if err != nil {
		return nil, apperr.Wrap(apperr.CodeExternal, "cannot read the target branch", "", err)
	}
	var skipped []string
	picks := make([]pick, 0, len(commits))
	for _, c := range commits {
		pk := pick{entry: c}
		if equivalent[c.FullHash] {
			pk.status = StatusApplied
			picks = append(picks, pk)
			continue
		}
		res, err := repo.SimulatePick(ctx, onto, c.FullHash)
		switch {
		case err != nil:
			pk.status = StatusError
			pk.conflicts = []string{strings.TrimSpace(err.Error())}
			skipped = append(skipped, c.Hash)
		case len(res.Conflicts) > 0:
			pk.status, pk.conflicts = StatusConflict, res.Conflicts
			skipped = append(skipped, c.Hash)
		case res.Tree == ontoTree:
			pk.status = StatusEmpty
		default:
			pk.status = StatusClean
			if onto, err = repo.CommitTree(ctx, res.Tree, onto); err != nil {
				return nil, apperr.Wrap(apperr.CodeExternal, "cannot record the simulated result", "", err)
			}
			ontoTree = res.Tree
		}
		if pk.status == StatusClean || pk.status == StatusEmpty {
			pk.skippedBefore = slices.Clone(skipped)
		}
		picks = append(picks, pk)
	}
	return picks, nil
}

// overlapping returns the files both branches changed since their merge base.
func overlapping(ctx context.Context, repo *repository.Repository, base, a, b string) ([]string, error) {
	fa, err := repo.ChangedFiles(ctx, base, a)
	if err != nil {
		return nil, err
	}
	fb, err := repo.ChangedFiles(ctx, base, b)
	if err != nil {
		return nil, err
	}
	var both []string
	for _, f := range fa {
		if slices.Contains(fb, f) {
			both = append(both, f)
		}
	}
	return both, nil
}

func renderCompare(p output.Printer, from, to, base string, picks []pick, behind []repository.LogEntry, overlap []string) {
	head := fmt.Sprintf("%s vs %s", p.Bold(from), p.Bold(to))
	if base != "" {
		head += p.Dim(fmt.Sprintf("  (merge base %s)", shortHash(base)))
	}
	p.Step("%s", head)
	p.Line("  %s is %s ahead and %s behind %s", from, commitCount(len(picks)), commitCount(len(behind)), to)

	if len(picks) > 0 {
		p.Line("")
		p.Line("%s  in %s, not in %s; tried on %s in this order", p.Bold("AHEAD"), from, to, to)
		rows := [][]string{{"COMMIT", "DATE", "AUTHOR", "MESSAGE", "CHERRY-PICK"}}
		for _, pk := range picks {
			rows = append(rows, []string{pk.entry.Hash, pk.entry.Date.Format("2006-01-02"), pk.entry.AuthorName, pk.entry.Subject, statusText(pk, to)})
		}
		p.Table(rows, func(row, col int, cell string) string {
			switch col {
			case 0:
				return p.Yellow(cell)
			case 1:
				return p.Dim(cell)
			case 4:
				switch picks[row-1].status {
				case StatusClean:
					return p.Green(cell)
				case StatusConflict, StatusError:
					return p.Red(cell)
				}
				return p.Dim(cell)
			}
			return cell
		})
	}

	if len(behind) > 0 {
		p.Line("")
		p.Line("%s  in %s, not in %s", p.Bold("BEHIND"), to, from)
		rows := [][]string{{"COMMIT", "DATE", "AUTHOR", "MESSAGE"}}
		for _, e := range behind {
			rows = append(rows, []string{e.Hash, e.Date.Format("2006-01-02"), e.AuthorName, e.Subject})
		}
		p.Table(rows, func(_, col int, cell string) string {
			switch col {
			case 0:
				return p.Yellow(cell)
			case 1:
				return p.Dim(cell)
			}
			return cell
		})
	}

	p.Line("")
	if len(overlap) > 0 {
		p.Info("changed on both branches: %s", strings.Join(overlap, ", "))
	}
	var clean []string
	for _, pk := range picks {
		if pk.status == StatusClean {
			clean = append(clean, pk.entry.Hash)
		}
	}
	// With conflicts, the error Compare returns is the summary line.
	switch {
	case len(picks) == 0:
		p.Success("nothing to apply: %s has no commits that %s lacks", from, to)
	case len(clean)+settled(picks) == len(picks):
		p.Success("all %d commit%s cherry-pick cleanly", len(picks), plural(len(picks)))
	}
	if len(clean) > 0 {
		p.Info("clean ones: git cherry-pick %s", strings.Join(clean, " "))
	}
}

// settled counts the commits that need no cherry-pick: already applied, or empty.
func settled(picks []pick) int {
	n := 0
	for _, pk := range picks {
		if pk.status == StatusApplied || pk.status == StatusEmpty {
			n++
		}
	}
	return n
}

func statusText(pk pick, to string) string {
	switch pk.status {
	case StatusClean:
		if len(pk.skippedBefore) > 0 {
			return "✓ clean (without " + strings.Join(pk.skippedBefore, ", ") + ")"
		}
		return "✓ clean"
	case StatusConflict:
		return "✗ conflict: " + strings.Join(pk.conflicts, ", ")
	case StatusApplied:
		return "≈ already in " + to
	case StatusEmpty:
		return "∅ no changes on " + to
	}
	return "✗ cannot simulate: " + strings.Join(pk.conflicts, " ")
}

func commitCount(n int) string {
	return fmt.Sprintf("%d commit%s", n, plural(n))
}

func shortHash(h string) string {
	if len(h) > 7 {
		return h[:7]
	}
	return h
}

type jsonPick struct {
	jsonCommit
	Status        string   `json:"status"`
	Conflicts     []string `json:"conflicts"`
	SkippedBefore []string `json:"skipped_before"`
}

func writeCompareJSON(p output.Printer, from, to, base string, picks []pick, behind []repository.LogEntry, overlap []string) error {
	out := struct {
		From        string       `json:"from"`
		To          string       `json:"to"`
		MergeBase   string       `json:"merge_base"`
		Ahead       []jsonPick   `json:"ahead"`
		Behind      []jsonCommit `json:"behind"`
		BothChanged []string     `json:"changed_on_both"`
	}{from, to, base, []jsonPick{}, []jsonCommit{}, orEmpty(overlap)}
	for _, pk := range picks {
		out.Ahead = append(out.Ahead, jsonPick{
			jsonCommit:    jsonCommit{pk.entry.Hash, pk.entry.AuthorName, pk.entry.AuthorEmail, pk.entry.Date, pk.entry.Subject, []jsonFile{}},
			Status:        pk.status,
			Conflicts:     orEmpty(pk.conflicts),
			SkippedBefore: orEmpty(pk.skippedBefore),
		})
	}
	for _, e := range behind {
		out.Behind = append(out.Behind, jsonCommit{e.Hash, e.AuthorName, e.AuthorEmail, e.Date, e.Subject, []jsonFile{}})
	}
	return encode(p, out)
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
