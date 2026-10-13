package app

import (
	"testing"

	"github.com/ashwin-y/mochi/internal/connector"
)

// sickConnector reports whether it can serve, as the test sets it.
type sickConnector struct {
	toggleConnector
	healthy bool
}

func (c *sickConnector) Healthy() bool { return c.healthy }

// A launcher that is on but not healthy stays on and is drawn yellow, not in the
// accent color; once it is healthy again it returns to the accent.
func TestUIUnhealthyServerTileIsYellow(t *testing.T) {
	sick := &sickConnector{toggleConnector: toggleConnector{name: "Sick"}}
	var _ connector.Connector = sick
	e := newUI(t, false, nil, sick)
	// The switch is on, as Listen would have left it. The test sets the app's
	// record directly: a click would start a health check that needs a window.
	e.app.serving[sick.Name()] = true
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
	if !e.app.serving[sick.Name()] {
		t.Fatal("the unhealthy launcher was switched off")
	}

	sick.healthy = true
	probe()
	if x, y := at(); !sameColor(e.pixel(x, y), e.theme.Accent) {
		t.Fatalf("a healthy listening tile is %v, want accent %v", e.pixel(x, y), e.theme.Accent)
	}
}
