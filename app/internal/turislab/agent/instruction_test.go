package agent

import (
	"strings"
	"testing"
)

func TestInstructionIncludesScreenshotRule(t *testing.T) {
	svc := &Service{cfg: Config{ArtifactsDir: "/srv/artifacts"}}
	got := svc.instruction()
	for _, want := range []string{
		"browser screenshot tool",
		"/srv/artifacts/<establishment_id>.png",
		"snapshot",
		"shown automatically as an image right below your answer",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("instruction missing %q", want)
		}
	}

	bare := (&Service{}).instruction()
	for _, bad := range []string{"browser screenshot tool", "shown automatically as an image"} {
		if strings.Contains(bare, bad) {
			t.Errorf("instruction without artifacts dir must not mention %q", bad)
		}
	}
}
