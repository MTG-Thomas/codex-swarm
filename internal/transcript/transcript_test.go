package transcript

import (
	"strings"
	"testing"

	"github.com/MTG-Thomas/codex-swarm/internal/store"
)

func TestTextUsesTranscriptPullRequests(t *testing.T) {
	transcript := Transcript{
		Worker: store.Worker{
			ID:           "w-1",
			PullRequests: []store.PullRequestState{{URL: "https://example.test/stale"}},
		},
		PullRequests: []store.PullRequestState{{URL: "https://example.test/snapshot"}},
	}

	text := transcript.Text()
	if !strings.Contains(text, "https://example.test/snapshot") {
		t.Fatalf("Text() = %q, want transcript PR snapshot", text)
	}
	if strings.Contains(text, "https://example.test/stale") {
		t.Fatalf("Text() = %q, contains worker PR state", text)
	}
}
