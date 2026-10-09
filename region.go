package main

import (
	"context"
	"errors"

	"github.com/tailscale/tailcat"
	"tailscale.com/tailcfg"
)

// PickRegion returns the ID of the DERP relay region nearest this machine. A
// server calls it once, saves the ID, and passes it as Server.RegionID on
// every start. Left to itself, Start probes every region's latency each time
// (about 10 s against the public relays) and may pick a different region, which
// changes the server's address. Do not read the ID back out of an address:
// tailcat renumbers an embedded region, so the ID it reports does not exist in
// the relay map.
func PickRegion(ctx context.Context) (tailcfg.DERPRegionID, error) {
	ci := &tailcat.ConnInfo{RegionID: -1}
	if err := ci.Expand(ctx); err != nil {
		return 0, err
	}
	if len(ci.Region) == 0 || ci.Region[0].RegionID <= 0 {
		return 0, errors.New("no relay region picked")
	}
	return ci.Region[0].RegionID, nil
}
