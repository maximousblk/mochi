package app

import (
	"testing"

	"github.com/ashwin-y/mochi/internal/connector"
)

// sickConnector is listening, but reports it cannot serve.
type sickConnector struct {
	toggleConnector
	healthy bool
}

func (c *sickConnector) Healthy() bool { return c.ServerEnabled() && c.healthy }

// A connector that is listening but not healthy stays on and is drawn yellow,
// not in the accent color; once it is healthy again it returns to the accent.
func TestUIUnhealthyServerTileIsYellow(t *testing.T) {
	sick := &sickConnector{toggleConnector: toggleConnector{name: "Sick", on: map[string]bool{}}}
	var _ connector.Connector = sick
	e := newUI(t, false, nil, sick)
	if err := sick.Listen(); err != nil {
		t.Fatal(err)
	}
	e.openSettings()
	revealInView(e.t, e.tt, "Sick launcher")
	tile, ok := e.tt.Find("Sick launcher")
	if !ok {
		t.Fatalf("no Sick tile; visible: %q", e.tt.Texts())
	}
	probe := func() { e.app.unhealthy = e.app.unhealthyNow(); e.tt.Frame() }
	at := func() (float32, float32) { return tile.X + 6, tile.Y + tile.H/2 }

	probe()
	if x, y := at(); !sameColor(e.pixel(x, y), e.theme.Warning) {
		t.Fatalf("an unhealthy listening tile is %v, want warning %v", e.pixel(x, y), e.theme.Warning)
	}
	if !sick.ServerEnabled() {
		t.Fatal("the unhealthy connector was switched off")
	}

	sick.healthy = true
	probe()
	if x, y := at(); !sameColor(e.pixel(x, y), e.theme.Accent) {
		t.Fatalf("a healthy listening tile is %v, want accent %v", e.pixel(x, y), e.theme.Accent)
	}
}
