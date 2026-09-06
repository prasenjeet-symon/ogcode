package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/db"
	"github.com/prasenjeet-symon/ogcode/internal/provider"
	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// imageProbeProvider stands in for an OpenAI(-compatible) provider so a test can
// control what the one-shot capability probe sees. It records how many times it
// was streamed and whether the last request actually carried an image.
type imageProbeProvider struct {
	id            string
	models        []provider.ModelInfo
	acceptImage   bool // true: probe succeeds (vision); false: image is rejected
	streamCalls   int
	lastReqImages int
}

func (m *imageProbeProvider) ID() string                   { return m.id }
func (m *imageProbeProvider) Models() []provider.ModelInfo { return m.models }

func (m *imageProbeProvider) StreamChat(ctx context.Context, req provider.StreamRequest) (<-chan provider.StreamEvent, error) {
	m.streamCalls++
	if len(req.Messages) > 0 {
		m.lastReqImages = len(req.Messages[0].Images)
	}
	ch := make(chan provider.StreamEvent, 4)
	go func() {
		defer close(ch)
		if m.acceptImage {
			ch <- provider.StreamEvent{Type: provider.EventTextDelta, Text: "ok"}
			fr := "stop"
			ch <- provider.StreamEvent{Type: provider.EventFinish, FinishReason: &fr}
			return
		}
		// A definitive image/modality rejection (classifyProbeError caches "no").
		ch <- provider.StreamEvent{Type: provider.EventError, Error: "this model does not support image input"}
	}()
	return ch, nil
}

func newImageSupportRunner(t *testing.T, p provider.Provider) (*LoopRunner, *db.DB) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "ogcode.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	reg := provider.NewRegistry()
	reg.Register(p)
	return &LoopRunner{Store: session.NewStore(database), Registry: reg}, database
}

// A custom model added under the "openai" slot is not in the curated catalog,
// so it must be probed rather than defaulting to "no images". Regression test
// for custom OpenAI models silently losing image support.
func TestResolveImageSupport_CustomOpenAIModelIsProbed(t *testing.T) {
	t.Run("vision model probes to true and caches", func(t *testing.T) {
		p := &imageProbeProvider{id: "openai", acceptImage: true}
		lr, database := newImageSupportRunner(t, p)

		const modelID = "gpt-4o-2024-11-20" // absent from the static OpenAI catalog
		lr.Registry.RegisterCustomModel(modelID, "openai")

		if !lr.resolveImageSupport(context.Background(), p, modelID) {
			t.Fatal("custom OpenAI vision model resolved to false; expected true after probe")
		}
		if p.streamCalls != 1 {
			t.Fatalf("expected exactly one probe call, got %d", p.streamCalls)
		}
		if p.lastReqImages == 0 {
			t.Fatal("probe request carried no image")
		}

		// The definitive probe result is cached: a second resolve reuses it and
		// does not spend another probe call.
		if !lr.resolveImageSupport(context.Background(), p, modelID) {
			t.Fatal("second resolve returned false; cached capability not honored")
		}
		if p.streamCalls != 1 {
			t.Fatalf("second resolve re-probed; stream called %d times, want 1", p.streamCalls)
		}
		cap, ok, err := session.GetModelCapability(database, modelID)
		if err != nil || !ok || !cap.SupportsImages {
			t.Fatalf("capability not cached as supported: ok=%v err=%v cap=%+v", ok, err, cap)
		}
	})

	t.Run("text-only model probes to false and caches", func(t *testing.T) {
		p := &imageProbeProvider{id: "openai", acceptImage: false}
		lr, database := newImageSupportRunner(t, p)

		const modelID = "some-text-only-custom-model"
		lr.Registry.RegisterCustomModel(modelID, "openai")

		if lr.resolveImageSupport(context.Background(), p, modelID) {
			t.Fatal("text-only custom model resolved to true; expected false after rejection")
		}
		if p.streamCalls != 1 {
			t.Fatalf("expected exactly one probe call, got %d", p.streamCalls)
		}
		cap, ok, err := session.GetModelCapability(database, modelID)
		if err != nil || !ok || cap.SupportsImages {
			t.Fatalf("rejection not cached as unsupported: ok=%v err=%v cap=%+v", ok, err, cap)
		}
	})
}

// A genuine built-in catalog model under the "openai" slot must keep trusting
// the curated catalog and must NOT spend a probe call — the fix for custom
// models should not regress the fast path.
func TestResolveImageSupport_BuiltInCatalogModelIsNotProbed(t *testing.T) {
	p := &imageProbeProvider{
		id: "openai",
		models: []provider.ModelInfo{
			{ID: "builtin-vision", ProviderID: "openai", SupportsImages: true},
		},
		acceptImage: false, // would resolve to false if it (wrongly) probed
	}
	lr, _ := newImageSupportRunner(t, p)

	if !lr.resolveImageSupport(context.Background(), p, "builtin-vision") {
		t.Fatal("built-in catalog vision model resolved to false")
	}
	if p.streamCalls != 0 {
		t.Fatalf("catalog model was probed (%d calls); expected the catalog short-circuit", p.streamCalls)
	}
}
