package agent

import (
	"strings"
	"testing"
)

func TestInstructionIncludesScreenshotRule(t *testing.T) {
	svc := &Service{cfg: Config{ArtifactsDir: "/srv/artifacts"}}
	got := svc.instruction()
	for _, want := range []string{
		"agent_browser_screenshot",
		"/srv/artifacts/<establishment_id>.png",
		"agent_browser_snapshot",
		"shown automatically as an image right below your answer",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("instruction missing %q", want)
		}
	}

	bare := (&Service{}).instruction()
	if strings.Contains(bare, "agent_browser_screenshot") {
		t.Error("instruction mentions screenshots without an artifacts directory")
	}
}
