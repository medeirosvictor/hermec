package client

import (
	"errors"
	"testing"

	"github.com/medeirosvictor/hermec/core/proto"
)

// A full dial to the standby is refused not_active, and the hosts frame that
// precedes the refusal comes back on the error so the caller can go straight
// to the active host.
func TestDialStandbyNotActiveCarriesHosts(t *testing.T) {
	p := newPair(t)
	_, err := Dial(testCtx(t), p.bURL, mustIdent(t), "alice", "")
	var se *ServerError
	if !errors.As(err, &se) || se.Code != proto.CodeNotActive {
		t.Fatalf("Dial standby = %v, want not_active", err)
	}
	if se.Hosts == nil || se.Hosts.Active.Fingerprint == "" {
		t.Fatalf("not_active carries no hosts: %+v", se.Hosts)
	}
}

func TestReportReach(t *testing.T) {
	p := newPair(t)
	c, err := Dial(testCtx(t), p.aURL, mustIdent(t), "alice", "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.ReportReach("someone", true); err != nil {
		t.Fatalf("ReportReach: %v", err)
	}
}
