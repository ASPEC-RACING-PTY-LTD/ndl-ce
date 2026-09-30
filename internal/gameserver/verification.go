package gameserver

// Verification levels, weakest first. A template's level is the strongest
// one that is actually true; the catalogue never upgrades a template on
// the strength of schema validation alone.
const (
	// VerifySchema: passes ValidateTemplate (automated catalogue checks).
	VerifySchema = "schema"
	// VerifySource: the upstream identifiers (Steam app ID, download URL,
	// GitHub repository and asset, ports, arguments) were cross-checked
	// against the reference named in SourceRef.
	VerifySource = "source"
	// VerifyInstalled: a real install completed in an isolated test
	// environment against the real upstream source.
	VerifyInstalled = "installed"
	// VerifyStarted: installed, and the server process started and stayed
	// up in an isolated test environment.
	VerifyStarted = "started"
)

// LiveResult records a real install/start test run.
type LiveResult struct {
	Level string `json:"level"`
	Date  string `json:"date"`
	Note  string `json:"note,omitempty"`
}

// liveVerified is maintained by hand from the output of the live test
// matrix (live_matrix_test.go, build tag livegs). Only add an entry after
// the run actually passed.
var liveVerified = map[string]LiveResult{
	// Isolated Docker daemon in a development sandbox. Each run downloaded
	// the real upstream artifact, started the server with the template's
	// startup line and stopped it with the template's stop command.
	"ndl-mindustry":         {Level: VerifyStarted, Date: "2026-09-30", Note: "GitHub release v146; stopped with exit"},
	"ndl-waterdog-pe":       {Level: VerifyStarted, Date: "2026-09-30", Note: "GitHub release v2.0.3; bound 19132/udp; stopped with end"},
	"ndl-powernukkitx":      {Level: VerifyStarted, Date: "2026-09-30", Note: "GitHub release 3.0.5 on Java 25; ready marker seen; stopped with stop"},
	"ndl-unciv":             {Level: VerifyStarted, Date: "2026-09-30", Note: "GitHub release 4.22.5 on Java 21"},
	"ndl-tmodloader":        {Level: VerifyStarted, Date: "2026-09-30", Note: "GitHub release v2026.07.3.0; stopped with exit"},
	"ndl-rimworld-together": {Level: VerifyStarted, Date: "2026-09-30", Note: "GitHub release 26.8.31.1 on .NET 8; stopped with quit"},
	"ndl-clonehero":         {Level: VerifyStarted, Date: "2026-09-30", Note: "GitHub release v1.1.0.6142-final on the .NET 8 runtime image"},
	"ndl-opensoldat":        {Level: VerifyStarted, Date: "2026-09-30", Note: "GitHub continuous build; runs as UID 1000; listening on 23073"},
}

// VerificationLevel returns the honest verification level of a template.
func VerificationLevel(t Template) string {
	if r, ok := liveVerified[t.ID]; ok && r.Level != "" {
		return r.Level
	}
	if t.SourceRef != "" {
		return VerifySource
	}
	return VerifySchema
}

// LiveVerification returns the recorded live test result, if any.
func LiveVerification(id string) (LiveResult, bool) {
	r, ok := liveVerified[id]
	return r, ok
}
