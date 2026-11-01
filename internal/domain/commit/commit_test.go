package commit

import "testing"

func TestValidate(t *testing.T) {
	tests := []struct {
		name  string
		msg   string
		rules Rules
		ok    bool
	}{
		{"simple", "feat: add thing\n", Rules{}, true},
		{"scope", "fix(api): handle nil\n\nbody", Rules{}, true},
		{"breaking", "feat(api)!: drop v1", Rules{}, true},
		{"comments ignored", "# comment\nchore: tidy\n# more", Rules{}, true},
		{"merge", "Merge branch 'stage' into feature/x", Rules{}, true},
		{"revert", "Revert \"feat: x\"", Rules{}, true},
		{"fixup", "fixup! feat: x", Rules{}, true},
		{"empty", "\n# only comments\n", Rules{}, false},
		{"scissors", "# ------------------------ >8 ------------------------\nfeat: x", Rules{}, false},
		{"no type", "add thing", Rules{}, false},
		{"unknown type", "wip: stuff", Rules{}, false},
		{"missing space", "feat:add", Rules{}, false},
		{"empty description", "feat: ", Rules{}, false},
		{"uppercase type", "Feat: x", Rules{}, false},
		{"scope required missing", "feat: x", Rules{RequireScope: true}, false},
		{"scope required present", "feat(ui): x", Rules{RequireScope: true}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.rules.Validate(tt.msg)
			if (err == nil) != tt.ok {
				t.Fatalf("Validate(%q) = %v, want ok=%v", tt.msg, err, tt.ok)
			}
		})
	}
}

func TestExample(t *testing.T) {
	for _, r := range []Rules{{}, {RequireScope: true}} {
		if err := r.Validate(r.Example()); err != nil {
			t.Errorf("example %q rejected by its own rules: %v", r.Example(), err)
		}
	}
}
