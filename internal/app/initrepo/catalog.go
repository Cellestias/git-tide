package initrepo

import (
	"fmt"
	"strings"
)

// catalogEntry is one ready-to-enable secret detector. Sample is a fake value
// the pattern must match; it is only used by tests.
type catalogEntry struct {
	Name    string
	Pattern string
	Sample  string
}

// catalogGroup collects detectors that belong together in the generated file.
type catalogGroup struct {
	Title   string
	Note    string
	Entries []catalogEntry
}

// j joins the parts of a fake sample. Samples are built from pieces so that
// no complete token-shaped string sits in the source for scanners to flag.
func j(parts ...string) string { return strings.Join(parts, "") }

func r(s string, n int) string { return strings.Repeat(s, n) }

var uuid = j(r("a", 8), "-", r("a", 4), "-", r("a", 4), "-", r("a", 4), "-", r("a", 12))

// secretCatalog is the list of detectors written, commented out, into a new
// secret.tide.yml. Tide already ships detectors for AWS access key IDs, GitHub
// tokens, Slack bot/user tokens, Stripe live keys, Google API keys and private
// key headers, so those are not repeated here. Every pattern is RE2 (Go).
var secretCatalog = []catalogGroup{
	{
		Title: "Cloud and infrastructure",
		Entries: []catalogEntry{
			{"AWS Secret Access Key", `(?i)aws.{0,16}(?:secret|sak).{0,16}['"=:\s]+[A-Za-z0-9/+=]{40}\b`, j(`aws_secret_access_key = "`, r("A", 40), `"`)},
			{"AWS MWS Key", `\bamzn\.mws\.[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`, j("amzn.mws.", uuid)},
			{"Azure Storage Account Key", `AccountKey=[A-Za-z0-9+/]{86}==`, j("AccountKey=", r("A", 86), "==")},
			{"Azure Storage Connection String", `DefaultEndpointsProtocol=https?;AccountName=[^;\s]+;AccountKey=`, "DefaultEndpointsProtocol=https;AccountName=acct;AccountKey=x"},
			{"Azure SAS Signature", `[?&]sig=[A-Za-z0-9%+/]{43,}%3D`, j("?sig=", r("A", 43), "%3D")},
			{"Azure AD Client Secret", `\b[A-Za-z0-9_~.-]{3}8Q~[A-Za-z0-9_~.-]{34}\b`, j("abc8Q~", r("a", 34))},
			{"Azure Service Bus Key", `Endpoint=sb://[^;\s]+;SharedAccessKeyName=[^;\s]+;SharedAccessKey=[A-Za-z0-9+/=]{20,}`, j("Endpoint=sb://ns.servicebus.windows.net/;SharedAccessKeyName=root;SharedAccessKey=", r("A", 24))},
			{"Google OAuth Client Secret", `\bGOCSPX-[A-Za-z0-9_-]{28}\b`, j("GOCSPX-", r("a", 28))},
			{"Google OAuth Access Token", `\bya29\.[0-9A-Za-z_-]{20,}`, j("ya29.", r("a", 24))},
			{"Google OAuth Refresh Token", `\b1//0[0-9A-Za-z_-]{38,}`, j("1//0", r("a", 40))},
			{"Google Service Account JSON", `"type"\s*:\s*"service_account"`, `"type": "service_account"`},
			{"Firebase Cloud Messaging Server Key", `\bAAAA[A-Za-z0-9_-]{7}:[A-Za-z0-9_-]{140}\b`, j("AAAA", r("a", 7), ":", r("a", 140))},
			{"DigitalOcean Personal Access Token", `\bdop_v1_[a-f0-9]{64}\b`, j("dop_v1_", r("a", 64))},
			{"DigitalOcean OAuth Token", `\bdoo_v1_[a-f0-9]{64}\b`, j("doo_v1_", r("a", 64))},
			{"DigitalOcean Refresh Token", `\bdor_v1_[a-f0-9]{64}\b`, j("dor_v1_", r("a", 64))},
			{"Alibaba Cloud AccessKey ID", `\bLTAI[A-Za-z0-9]{20}\b`, j("LTAI", r("a", 20))},
			{"Tencent Cloud SecretId", `\bAKID[A-Za-z0-9]{32}\b`, j("AKID", r("a", 32))},
			{"Yandex Cloud IAM Key", `\bAQVN[A-Za-z0-9_-]{35,38}\b`, j("AQVN", r("a", 36))},
			{"Heroku Platform API Key", `\bHRKU-[A-Za-z0-9_-]{60}\b`, j("HRKU-", r("a", 60))},
			{"Heroku API Key by Context", `(?i)heroku.{0,20}[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`, j("HEROKU_API_KEY=", uuid)},
			{"Cloudflare API Token by Context", `(?i)cloudflare.{0,20}['"=:\s][A-Za-z0-9_-]{40}\b`, j(`CLOUDFLARE_API_TOKEN="`, r("a", 40))},
			{"Cloudflare Origin CA Key", `\bv1\.0-[a-f0-9]{24}-[a-f0-9]{146}\b`, j("v1.0-", r("a", 24), "-", r("a", 146))},
			{"Fly.io Access Token", `\bfo1_[A-Za-z0-9_-]{43}\b`, j("fo1_", r("a", 43))},
			{"Render API Key", `\brnd_[A-Za-z0-9]{32}\b`, j("rnd_", r("a", 32))},
			{"Supabase Personal Access Token", `\bsbp_[a-f0-9]{40}\b`, j("sbp_", r("a", 40))},
			{"Neon API Key", `\bnapi_[a-z0-9]{60,}\b`, j("napi_", r("a", 64))},
			{"PlanetScale API Token", `\bpscale_tkn_[A-Za-z0-9_.-]{32,64}\b`, j("pscale_tkn_", r("a", 40))},
			{"PlanetScale Password", `\bpscale_pw_[A-Za-z0-9_.-]{32,64}\b`, j("pscale_pw_", r("a", 40))},
			{"Databricks Access Token", `\bdapi[a-h0-9]{32}\b`, j("dapi", r("a", 32))},
			{"HashiCorp Vault Service Token", `\bhvs\.[A-Za-z0-9_-]{24,}\b`, j("hvs.", r("a", 28))},
			{"HashiCorp Vault Batch Token", `\bhvb\.[A-Za-z0-9_-]{24,}\b`, j("hvb.", r("a", 28))},
			{"Terraform Cloud API Token", `\b[A-Za-z0-9]{14}\.atlasv1\.[A-Za-z0-9_-]{60,70}\b`, j(r("a", 14), ".atlasv1.", r("a", 64))},
			{"Doppler Token", `\bdp\.(?:pt|st|sa|ct|scim|audit)\.[A-Za-z0-9]{40,44}\b`, j("dp.pt.", r("a", 40))},
			{"Pulumi Access Token", `\bpul-[a-f0-9]{40}\b`, j("pul-", r("a", 40))},
			{"Tailscale Key", `\btskey-(?:auth|api|client)-[A-Za-z0-9]{10,}-[A-Za-z0-9]{20,}\b`, j("tskey-auth-", r("a", 12), "-", r("a", 24))},
			{"1Password Service Account Token", `\bops_eyJ[A-Za-z0-9+/=]{250,}`, j("ops_eyJ", r("a", 260))},
			{"Elastic Cloud API Key", `\bessu_[A-Za-z0-9+/=]{60,}`, j("essu_", r("a", 64))},
			{"Scalingo API Token", `\btk-us-[A-Za-z0-9_-]{48}\b`, j("tk-us-", r("a", 48))},
			{"Infracost API Key", `\bico-[A-Za-z0-9]{32}\b`, j("ico-", r("a", 32))},
			{"Prefect API Key", `\bpnu_[A-Za-z0-9]{36}\b`, j("pnu_", r("a", 36))},
			{"Cloudinary URL", `cloudinary://\d+:[A-Za-z0-9_-]+@`, "cloudinary://123456789012345:abcdefg_hij@cloud"},
			{"S3 Presigned URL Signature", `X-Amz-Signature=[a-f0-9]{64}`, j("X-Amz-Signature=", r("a", 64))},
			{"GCS Signed URL Signature", `X-Goog-Signature=[a-f0-9]{128,}`, j("X-Goog-Signature=", r("a", 128))},
		},
	},
	{
		Title: "Source control, CI/CD and package registries",
		Note:  "GitHub tokens are already detected by default.",
		Entries: []catalogEntry{
			{"GitLab Personal Access Token", `\bglpat-[A-Za-z0-9_-]{20}\b`, j("glpat-", r("a", 20))},
			{"GitLab Deploy Token", `\bgldt-[A-Za-z0-9_-]{20}\b`, j("gldt-", r("a", 20))},
			{"GitLab Runner Token", `\bglrt-[A-Za-z0-9_-]{20}\b`, j("glrt-", r("a", 20))},
			{"GitLab Pipeline Trigger Token", `\bglptt-[a-f0-9]{40}\b`, j("glptt-", r("a", 40))},
			{"GitLab Feed Token", `\bglft-[A-Za-z0-9_-]{20}\b`, j("glft-", r("a", 20))},
			{"GitLab CI Job Token", `\bglcbt-[A-Za-z0-9_-]{20,}\b`, j("glcbt-", r("a", 24))},
			{"GitLab Agent Token", `\bglagent-[A-Za-z0-9_-]{50,}\b`, j("glagent-", r("a", 52))},
			{"GitLab SCIM Token", `\bglsoat-[A-Za-z0-9_-]{20}\b`, j("glsoat-", r("a", 20))},
			{"GitLab Incoming Mail Token", `\bglimt-[A-Za-z0-9_-]{25}\b`, j("glimt-", r("a", 25))},
			{"Bitbucket Access Token", `\bATBB[A-Za-z0-9_=.-]{32,}`, j("ATBB", r("a", 36))},
			{"Atlassian API Token", `\bATATT3[A-Za-z0-9_=-]{180,}`, j("ATATT3", r("a", 190))},
			{"CircleCI Personal Access Token", `\bCCIPAT_[A-Za-z0-9]{22}_[a-f0-9]{40}\b`, j("CCIPAT_", r("a", 22), "_", r("a", 40))},
			{"Buildkite Agent Token", `\bbkua_[a-f0-9]{40}\b`, j("bkua_", r("a", 40))},
			{"SonarQube Token", `\bsq[pua]_[a-f0-9]{40}\b`, j("sqp_", r("a", 40))},
			{"npm Access Token", `\bnpm_[A-Za-z0-9]{36}\b`, j("npm_", r("a", 36))},
			{"npmrc Auth Token", `//[^\s:]+/:_authToken=[A-Za-z0-9._-]{20,}`, j("//registry.npmjs.org/:_authToken=", r("a", 24))},
			{"PyPI Upload Token", `\bpypi-AgEIcHlwaS5vcmc[A-Za-z0-9_-]{50,}`, j("pypi-AgEIcHlwaS5vcmc", r("a", 60))},
			{"RubyGems API Key", `\brubygems_[a-f0-9]{48}\b`, j("rubygems_", r("a", 48))},
			{"NuGet API Key", `\boy2[a-z0-9]{43}\b`, j("oy2", r("a", 43))},
			{"crates.io Token", `\bcio[A-Za-z0-9]{32}\b`, j("cio", r("a", 32))},
			{"Clojars Deploy Token", `\bCLOJARS_[a-z0-9]{60}\b`, j("CLOJARS_", r("a", 60))},
			{"Docker Hub Personal Access Token", `\bdckr_pat_[A-Za-z0-9_-]{27,}\b`, j("dckr_pat_", r("a", 27))},
			{"Sourcegraph Access Token", `\bsgp_(?:[a-f0-9]{16}_)?[a-f0-9]{40}\b`, j("sgp_", r("a", 16), "_", r("a", 40))},
			{"Postman API Key", `\bPMAK-[a-f0-9]{24}-[a-f0-9]{34}\b`, j("PMAK-", r("a", 24), "-", r("a", 34))},
			{"Ansible Vault Header", `\$ANSIBLE_VAULT;1\.[12];AES256`, "$ANSIBLE_VAULT;1.1;AES256"},
			{"Kubernetes Client Key Data", `client-key-data:\s*[A-Za-z0-9+/=]{100,}`, j("client-key-data: ", r("A", 120))},
			{"Docker Config Auth", `"auth"\s*:\s*"[A-Za-z0-9+/=]{20,}"`, j(`"auth": "`, r("A", 24), `"`)},
		},
	},
	{
		Title: "Observability and developer tools",
		Entries: []catalogEntry{
			{"Datadog API or App Key by Context", `(?i)(?:datadog|dd)[_-]?(?:api|app)[_-]?key.{0,5}['"=:\s]+[a-f0-9]{32,40}\b`, j("DD_API_KEY=", r("a", 32))},
			{"New Relic User API Key", `\bNRAK-[A-Z0-9]{27}\b`, j("NRAK-", r("A", 27))},
			{"New Relic Ingest Key", `\bNRII-[A-Za-z0-9-]{32}\b`, j("NRII-", r("a", 32))},
			{"New Relic Browser Key", `\bNRJS-[a-f0-9]{19}\b`, j("NRJS-", r("a", 19))},
			{"Sentry Auth Token", `\bsntrys_[A-Za-z0-9+/=_-]{60,}`, j("sntrys_", r("a", 64))},
			{"Sentry DSN", `https://[a-f0-9]{32}@(?:[a-z0-9.-]+\.)?sentry\.io/\d+`, j("https://", r("a", 32), "@o1.ingest.sentry.io/123")},
			{"Dynatrace API Token", `\bdt0c01\.[A-Z0-9]{24}\.[A-Z0-9]{64}\b`, j("dt0c01.", r("A", 24), ".", r("A", 64))},
			{"Grafana Cloud API Token", `\bglc_[A-Za-z0-9+/]{32,}={0,2}`, j("glc_", r("a", 40))},
			{"Grafana Service Account Token", `\bglsa_[A-Za-z0-9]{32}_[A-Fa-f0-9]{8}\b`, j("glsa_", r("a", 32), "_", r("a", 8))},
			{"PostHog Project API Key", `\bphc_[A-Za-z0-9]{43}\b`, j("phc_", r("a", 43))},
			{"PostHog Personal API Key", `\bphx_[A-Za-z0-9]{43}\b`, j("phx_", r("a", 43))},
			{"LaunchDarkly Key", `\b(?:api|sdk)-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`, j("sdk-", uuid)},
			{"Linear API Key", `\blin_api_[A-Za-z0-9]{40}\b`, j("lin_api_", r("a", 40))},
			{"Notion Integration Token", `\b(?:ntn|secret)_[A-Za-z0-9]{40,}\b`, j("ntn_", r("a", 44))},
			{"Airtable Personal Access Token", `\bpat[A-Za-z0-9]{14}\.[a-f0-9]{64}\b`, j("pat", r("a", 14), ".", r("a", 64))},
			{"Figma Personal Access Token", `\bfigd_[A-Za-z0-9_-]{40,}\b`, j("figd_", r("a", 42))},
			{"Contentful Personal Access Token", `\bCFPAT-[A-Za-z0-9_-]{43}\b`, j("CFPAT-", r("a", 43))},
			{"Typeform Personal Token", `\btfp_[A-Za-z0-9_-]{40,}\b`, j("tfp_", r("a", 44))},
			{"Dropbox Access Token", `\bsl\.[A-Za-z0-9_-]{130,}`, j("sl.", r("a", 140))},
			{"Okta API Token by Context", `(?i)okta.{0,20}['"=:\s]00[A-Za-z0-9_-]{40}\b`, j(`OKTA_API_TOKEN="00`, r("a", 40))},
			{"Readme API Key", `\brdme_[a-z0-9]{70}\b`, j("rdme_", r("a", 70))},
			{"Frame.io Token", `\bfio-u-[A-Za-z0-9_=-]{64}\b`, j("fio-u-", r("a", 64))},
		},
	},
	{
		Title: "Messaging and email",
		Note:  "Slack bot and user tokens (xoxb, xoxp, ...) are already detected by default.",
		Entries: []catalogEntry{
			{"Slack Webhook URL", `https://hooks\.slack\.com/(?:services|workflows|triggers)/[A-Za-z0-9/_-]{20,}`, j("https://hooks.slack.com/services/", r("A", 24))},
			{"Slack App-Level Token", `\bxapp-\d-[A-Z0-9]+-\d+-[a-f0-9]+\b`, j("xapp-1-A", r("B", 8), "-1234567890-", r("a", 16))},
			{"Slack Configuration Token", `\bxoxe(?:\.xox[bp])?-\d-[A-Za-z0-9]{100,}`, j("xoxe-1-", r("a", 110))},
			{"Discord Bot Token", `\b[MNO][A-Za-z0-9_-]{23,25}\.[A-Za-z0-9_-]{6}\.[A-Za-z0-9_-]{27,38}\b`, j("M", r("a", 23), ".", r("a", 6), ".", r("a", 30))},
			{"Discord Webhook URL", `https://(?:ptb\.|canary\.)?discord(?:app)?\.com/api/webhooks/\d{17,20}/[A-Za-z0-9_-]{60,}`, j("https://discord.com/api/webhooks/", r("1", 18), "/", r("a", 64))},
			{"Telegram Bot Token", `\b\d{8,10}:AA[A-Za-z0-9_-]{33}\b`, j("123456789:AA", r("a", 33))},
			{"Twilio API Key SID", `\bSK[0-9a-f]{32}\b`, j("SK", r("a", 32))},
			{"Twilio Auth Token by Context", `(?i)twilio.{0,20}['"=:\s][0-9a-f]{32}\b`, j(`TWILIO_AUTH_TOKEN="`, r("a", 32))},
			{"SendGrid API Key", `\bSG\.[A-Za-z0-9_-]{22}\.[A-Za-z0-9_-]{43}\b`, j("SG.", r("a", 22), ".", r("a", 43))},
			{"Mailgun Private Key", `\bkey-[0-9a-f]{32}\b`, j("key-", r("a", 32))},
			{"Mailchimp API Key", `\b[0-9a-f]{32}-us\d{1,2}\b`, j(r("a", 32), "-us12")},
			{"Brevo Sendinblue API Key", `\bxkeysib-[a-f0-9]{64}-[A-Za-z0-9]{16}\b`, j("xkeysib-", r("a", 64), "-", r("a", 16))},
			{"Resend API Key", `\bre_[A-Za-z0-9]{8,}_[A-Za-z0-9]{20,}\b`, j("re_", r("a", 8), "_", r("a", 24))},
		},
	},
	{
		Title: "Payments and commerce",
		Note:  "Stripe live keys are already detected by default.",
		Entries: []catalogEntry{
			{"Stripe Test Key", `\b[sr]k_test_[0-9A-Za-z]{24,}\b`, j("sk_test_", r("a", 24))},
			{"Stripe Webhook Secret", `\bwhsec_[A-Za-z0-9]{32,}\b`, j("whsec_", r("a", 32))},
			{"Braintree Access Token", `\baccess_token\$production\$[a-z0-9]{16}\$[a-f0-9]{32}\b`, j("access_token$production$", r("a", 16), "$", r("a", 32))},
			{"Square Access Token", `\bEAAA[A-Za-z0-9_-]{60}\b`, j("EAAA", r("a", 60))},
			{"Square OAuth Secret", `\bsq0csp-[A-Za-z0-9_-]{43}\b`, j("sq0csp-", r("a", 43))},
			{"Square Application Token", `\bsq0atp-[A-Za-z0-9_-]{22}\b`, j("sq0atp-", r("a", 22))},
			{"Shopify Access Token", `\bshp(?:at|ss|ca|pa)_[a-fA-F0-9]{32}\b`, j("shpat_", r("a", 32))},
			{"Plaid Access Token", `\baccess-(?:sandbox|development|production)-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`, j("access-production-", uuid)},
			{"Razorpay Key ID", `\brzp_(?:live|test)_[A-Za-z0-9]{14}\b`, j("rzp_live_", r("a", 14))},
			{"Mercado Pago Access Token", `\bAPP_USR-\d{12,16}-\d{6}-[a-f0-9]{32}-\d+\b`, j("APP_USR-", r("1", 14), "-", r("2", 6), "-", r("a", 32), "-12345")},
			{"Paystack Secret Key", `\bsk_(?:live|test)_[a-f0-9]{40}\b`, j("sk_live_", r("a", 40))},
			{"Flutterwave Secret Key", `\bFLWSECK(?:_TEST)?-[a-f0-9]{32}-X\b`, j("FLWSECK-", r("a", 32), "-X")},
			{"Duffel Access Token", `\bduffel_(?:test|live)_[A-Za-z0-9_=-]{43}\b`, j("duffel_live_", r("a", 43))},
			{"EasyPost API Key", `\bEZ[AT]K[a-z0-9]{54}\b`, j("EZAK", r("a", 54))},
			{"Shippo API Token", `\bshippo_(?:live|test)_[a-f0-9]{40}\b`, j("shippo_live_", r("a", 40))},
		},
	},
	{
		Title: "AI and machine learning",
		Entries: []catalogEntry{
			{"OpenAI API Key", `\bsk-(?:proj-|svcacct-|admin-)?[A-Za-z0-9_-]{20,}T3BlbkFJ[A-Za-z0-9_-]{20,}\b`, j("sk-", r("a", 24), "T3Blbk", "FJ", r("a", 24))},
			{"OpenAI Project Key", `\bsk-(?:proj|svcacct)-[A-Za-z0-9_-]{40,}\b`, j("sk-proj-", r("a", 48))},
			{"Anthropic API Key", `\bsk-ant-(?:api|admin)\d{2}-[A-Za-z0-9_-]{80,}\b`, j("sk-ant-api03-", r("a", 84))},
			{"Hugging Face Token", `\bhf_[A-Za-z0-9]{34}\b`, j("hf_", r("a", 34))},
			{"Replicate API Token", `\br8_[A-Za-z0-9]{37}\b`, j("r8_", r("a", 37))},
			{"Groq API Key", `\bgsk_[A-Za-z0-9]{52}\b`, j("gsk_", r("a", 52))},
			{"Perplexity API Key", `\bpplx-[A-Za-z0-9]{48}\b`, j("pplx-", r("a", 48))},
			{"xAI API Key", `\bxai-[A-Za-z0-9]{80}\b`, j("xai-", r("a", 80))},
			{"OpenRouter API Key", `\bsk-or-v1-[a-f0-9]{64}\b`, j("sk-or-v1-", r("a", 64))},
			{"Pinecone API Key", `\bpcsk_[A-Za-z0-9_]{50,}\b`, j("pcsk_", r("a", 56))},
			{"LangSmith API Key", `\blsv2_(?:pt|sk)_[a-f0-9]{32}_[a-f0-9]{10}\b`, j("lsv2_pt_", r("a", 32), "_", r("a", 10))},
			{"Mapbox Secret Token", `\bsk\.eyJ1Ijoi[A-Za-z0-9_-]{40,}\.[A-Za-z0-9_-]{20,}\b`, j("sk.eyJ1Ijoi", r("a", 44), ".", r("a", 22))},
		},
	},
	{
		Title: "Social networks",
		Entries: []catalogEntry{
			{"Facebook Access Token", `\bEAA[A-Za-z0-9]{90,}\b`, j("EAA", r("a", 100))},
			{"Twitter X Bearer Token", `\bAAAAAAAAAAAAAAAAAAAAA[A-Za-z0-9%]{30,}`, j(r("A", 21), r("a", 40))},
		},
	},
	{
		Title: "Credentials embedded in text",
		Note:  "Generic detectors. Useful, but they can flag test fixtures: review before enabling.",
		Entries: []catalogEntry{
			{"JSON Web Token", `\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`, j("eyJ", r("a", 12), ".eyJ", r("a", 12), ".", r("a", 12))},
			{"Database URL with Password", `(?i)\b(?:postgres(?:ql)?|mysql|mariadb|mongodb(?:\+srv)?|redis|rediss|amqps?|mssql|sqlserver)://[^/\s:@]+:[^/\s:@]+@`, "postgres://app:hunter22@db.internal/app"},
			{"Password in URL", `\b[a-z][a-z0-9+.-]*://[^/\s:@]{1,64}:[^/\s:@]{3,128}@[A-Za-z0-9.-]+`, "https://user:hunter22@example.com"},
			{"Authorization Bearer Header", `(?i)\bauthorization\s*[:=]\s*['"]?bearer\s+[A-Za-z0-9._~+/=-]{20,}`, j("Authorization: Bearer ", r("a", 24))},
			{"Authorization Basic Header", `(?i)\bauthorization\s*[:=]\s*['"]?basic\s+[A-Za-z0-9+/=]{16,}`, j("Authorization: Basic ", r("A", 20))},
			{"Hard-coded Credential Assignment", `(?i)\b(?:password|passwd|pwd|secret|api[_-]?key|access[_-]?token|auth[_-]?token|client[_-]?secret)\b\s*[:=]\s*['"][^'"\s]{12,}['"]`, `password = "hunter2hunter2"`},
			{"age Secret Key", `\bAGE-SECRET-KEY-1[QPZRY9X8GF2TVDW0S3JN54KHCE6MUA7L]{58}\b`, j("AGE-SECRET-KEY-1", r("Q", 58))},
			{"PuTTY Private Key", `PuTTY-User-Key-File-\d`, "PuTTY-User-Key-File-3: ssh-ed25519"},
		},
	},
}

// catalogSize is the number of detectors in the catalog.
func catalogSize() int {
	n := 0
	for _, g := range secretCatalog {
		n += len(g.Entries)
	}
	return n
}

// renderCatalog writes the catalog as commented YAML list items, indented to sit
// under `secrets.patterns`. Enabling one is deleting the leading "# " of its two lines.
func renderCatalog() string {
	var b strings.Builder
	for gi, g := range secretCatalog {
		if gi > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "    # --- %s ---\n", g.Title)
		if g.Note != "" {
			fmt.Fprintf(&b, "    # %s\n", g.Note)
		}
		for _, e := range g.Entries {
			fmt.Fprintf(&b, "    # - name: %s\n", e.Name)
			fmt.Fprintf(&b, "    #   pattern: '%s'\n", strings.ReplaceAll(e.Pattern, "'", "''"))
		}
	}
	return b.String()
}
