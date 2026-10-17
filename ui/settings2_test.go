package ui

import (
	"context"
	"errors"
	"image"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/medeirosvictor/hermec/ui/state"
	"github.com/medeirosvictor/hermec/ui/theme"
	"github.com/medeirosvictor/hermec/update"
)

var errFake = errors.New("fake")

// ---- font size ----

func TestSizeRowIsAfterFont(t *testing.T) {
	if rowSize != rowFont+1 {
		t.Fatalf("rowSize=%d rowFont=%d", rowSize, rowFont)
	}
}

func TestSizeRowCyclesRebuildsAndPersists(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.toml")
	g := testGame(t)
	g.settingsPath = p
	g.set = state.Settings{Font: "vt323"}
	base := g.th.FontSize
	g.activateSetting(rowSize) // unset = 100 -> 110
	if g.set.FontScale != 110 {
		t.Fatalf("FontScale = %d", g.set.FontScale)
	}
	if g.face.Size != base*1.1 || g.faceS.Size != base*1.1*smallScale || g.faceT.Size != base*1.1*titleScale {
		t.Fatalf("sizes %v %v %v", g.face.Size, g.faceS.Size, g.faceT.Size)
	}
	if g.fontSize() != base*1.1 || g.lineH() != base*1.1*1.6 {
		t.Fatalf("fontSize %v lineH %v", g.fontSize(), g.lineH())
	}
	if s, err := state.LoadSettings(p); err != nil || s.FontScale != 110 {
		t.Fatalf("persisted %+v %v", s, err)
	}
	g.set.FontScale = 150
	g.activateSetting(rowSize) // wraps
	if g.set.FontScale != 80 || g.face.Size != base*0.8 {
		t.Fatalf("wrap: %d %v", g.set.FontScale, g.face.Size)
	}
}

// One helper builds the faces for startup, the font cycle, the size cycle and
// the dev theme reload.
func TestBuildFacesUsesScale(t *testing.T) {
	f, fs, ft, err := buildFaces("vt323", 18, 125)
	if err != nil {
		t.Skip(err)
	}
	if f.Size != 18*1.25 || fs.Size != 18*1.25*smallScale || ft.Size != 18*1.25*titleScale {
		t.Fatalf("%v %v %v", f.Size, fs.Size, ft.Size)
	}
	g := testGame(t)
	g.set = state.Settings{FontScale: 90}
	if err := g.applyDevTheme(theme.Theme{FontSize: 20}); err != nil {
		t.Skip(err)
	}
	if g.face.Size != 20*0.9 {
		t.Fatalf("applyDevTheme face size %v", g.face.Size)
	}
}

func TestScaleIsPartOfCacheKeys(t *testing.T) {
	g := &game{th: theme.Default(), set: state.Settings{Font: "vt323"}}
	var lc state.LayoutCache
	msgs := []state.Message{{FromName: "a", Text: "hi", TS: time.Unix(1, 0)}}
	g.relayout(&lc, "c", msgs, 40)
	if _, relaid := g.relayout(&lc, "c", msgs, 40); relaid {
		t.Fatal("same scale relaid")
	}
	g.set.FontScale = 110
	if _, relaid := g.relayout(&lc, "c", msgs, 40); !relaid {
		t.Fatal("scale change did not re-layout")
	}
	r := image.Rect(0, 0, 100, 100)
	gm := geometry{adv: 9, lh: 20}
	g.set.FontScale = 100
	a := g.sbKeyFor(1, 0, r, gm)
	g.set.FontScale = 110
	if b := g.sbKeyFor(1, 0, r, gm); a == b {
		t.Fatal("sbKey equal for different scales with equal adv/lh/colours")
	}
}

// ---- update row ----

func TestUpdateRowLabel(t *testing.T) {
	av := &update.Available{Version: "v1.2.3"}
	cases := []struct {
		st   updateStatus
		a    *update.Available
		ver  string
		want string
	}{
		{updChecking, nil, "v1.0.0", "update:    checking…"},
		{updUpToDate, nil, "v1.0.0", "update:    up to date (v1.0.0)"},
		{updUpToDate, nil, "dev", "update:    up to date (dev)"},
		{updUpToDate, nil, "", "update:    up to date (dev)"},
		{updAvailable, av, "v1.0.0", "update:    v1.2.3 available ›"},
		{updFailed, nil, "v1.0.0", "update:    check failed"},
	}
	for _, c := range cases {
		if got := updateRowLabel(c.st, c.a, c.ver); got != c.want {
			t.Errorf("updateRowLabel(%v) = %q, want %q", c.st, got, c.want)
		}
	}
}

func TestUpdateRowOpensWhenAvailable(t *testing.T) {
	g := testGame(t)
	var opened []string
	g.openFn = func(u string) { opened = append(opened, u) }
	g.checkFn = func(context.Context, string) (update.Available, bool, error) {
		t.Error("must not re-check when an update is available")
		return update.Available{}, false, nil
	}
	g.update = &update.Available{Version: "v9.9.9", URL: "https://github.com/medeirosvictor/hermec/releases/tag/v9.9.9"}
	g.updStatus = updAvailable
	g.activateSetting(rowUpdate)
	if len(opened) != 1 || opened[0] != g.update.URL {
		t.Fatalf("opened %v", opened)
	}
}

func TestUpdateRowRechecksOtherwise(t *testing.T) {
	g := testGame(t)
	g.ver = "v1.0.0"
	g.updateCh = make(chan updateResult, 1)
	g.openFn = func(string) { t.Error("must not open a page without an update") }
	called := make(chan string, 1)
	g.checkFn = func(_ context.Context, cur string) (update.Available, bool, error) {
		called <- cur
		return update.Available{}, false, nil
	}
	g.updStatus = updFailed
	g.activateSetting(rowUpdate)
	if g.updStatus != updChecking {
		t.Fatalf("status = %v, want checking", g.updStatus)
	}
	select {
	case cur := <-called:
		if cur != "v1.0.0" {
			t.Fatalf("checked against %q", cur)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("re-check never ran")
	}
	// While one is in flight, another activation does not start a second.
	g.activateSetting(rowUpdate)
	deadline := time.Now().Add(2 * time.Second)
	for g.updStatus == updChecking && time.Now().Before(deadline) {
		g.pollUpdate()
		time.Sleep(time.Millisecond)
	}
	if g.updStatus != updUpToDate {
		t.Fatalf("status = %v, want up to date", g.updStatus)
	}
	select {
	case <-called:
		t.Fatal("second check started while one was in flight")
	default:
	}
}

func TestPollUpdateStates(t *testing.T) {
	g := testGame(t)
	g.updateCh = make(chan updateResult, 1)
	g.updateCh <- updateResult{err: errFake}
	g.pollUpdate()
	if g.updStatus != updFailed || g.update != nil {
		t.Fatalf("failed: %v %v", g.updStatus, g.update)
	}
	g.updateCh <- updateResult{a: update.Available{Version: "v2"}, found: true}
	g.pollUpdate()
	if g.updStatus != updAvailable || g.update == nil || g.update.Version != "v2" {
		t.Fatalf("available: %v %v", g.updStatus, g.update)
	}
}

// ---- fingerprint tooltip ----

func TestFingerprintTooltip(t *testing.T) {
	g := testGame(t)
	g.fp = "abcd1234"
	g.settingsOpen = true
	g.buildHits()
	lh := g.lineH()
	y := connectFieldsTop(lh) + float64(settingsRows+1)*lh + 1
	x := connectX + g.railW() + 5
	h, ok := g.hits.At(x, y)
	if !ok || h.Kind != state.HitFingerprint {
		t.Fatalf("hit = %+v %v", h, ok)
	}
	if _, ok := g.hits.At(connectX+g.railW()+g.glyphW()*float64(len("fingerprint: abcd1234"))+20, y); ok {
		t.Fatal("hit rect extends past the text")
	}
	t0 := time.Now()
	g.stepTooltip(h, true, t0)
	if _, ok := g.tooltipText(); ok {
		t.Fatal("tooltip before the delay")
	}
	g.stepTooltip(h, true, t0.Add(state.TooltipDelay))
	got, ok := g.tooltipText()
	if !ok || got != fingerprintTip {
		t.Fatalf("tooltip = %q %v", got, ok)
	}
	if !strings.HasPrefix(fingerprintTip, "Your fingerprint is a short code made from your identity key. ") ||
		!strings.HasSuffix(fingerprintTip, "compare it with a friend out loud.") {
		t.Fatalf("copy changed: %q", fingerprintTip)
	}
	lines := tooltipLines(fingerprintTip, 30)
	if len(lines) < 2 || strings.Join(lines, " ") != fingerprintTip {
		t.Fatalf("lines = %q", lines)
	}
	for _, l := range lines {
		if len([]rune(l)) > 30 {
			t.Errorf("line over 30 cols: %q", l)
		}
	}
}

func TestFingerprintHitOnConnectScene(t *testing.T) {
	g := testGame(t)
	g.fp = "abcd1234"
	g.buildHits()
	lh := g.lineH()
	y := g.connect.fpTop(lh) + 1
	if h, ok := g.hits.At(connectX+g.railW()+5, y); !ok || h.Kind != state.HitFingerprint {
		t.Fatalf("hit = %+v %v", h, ok)
	}
	g.connect.disc = make([]state.DiscoveredRow, 2)
	g.buildHits()
	y = g.connect.fpTop(lh) + 1
	if h, ok := g.hits.At(connectX+g.railW()+5, y); !ok || h.Kind != state.HitFingerprint {
		t.Fatalf("with discovered rows: hit = %+v %v", h, ok)
	}
}

// ---- help ----

func TestHelpTextParses(t *testing.T) {
	lines := parseHelp(helpSrc)
	var heads []string
	for _, l := range lines {
		if l.Heading {
			heads = append(heads, l.Text)
		}
		if strings.HasPrefix(l.Text, "# ") {
			t.Errorf("heading marker left in %q", l.Text)
		}
	}
	want := []string{"What HERMEC is", "Your identity and fingerprint", "Servers", "Reaching your friends", "Voice", "Keys"}
	if strings.Join(heads, "|") != strings.Join(want, "|") {
		t.Fatalf("headings = %q", heads)
	}
	if lines[0].Text != "What HERMEC is" || !lines[0].Heading {
		t.Fatalf("first = %+v", lines[0])
	}
	blanks, paras := 0, 0
	for _, l := range lines {
		switch {
		case l.Text == "" && !l.Heading:
			blanks++
		case !l.Heading:
			paras++
		}
	}
	if blanks < 5 || paras < 5 {
		t.Fatalf("blanks=%d paragraphs=%d", blanks, paras)
	}
	if !strings.Contains(helpSrc, "Ctrl+M") {
		t.Fatal("Keys section missing")
	}
}

func TestParseHelpSyntax(t *testing.T) {
	got := parseHelp("# A\nbody one\n\nbody two\r\n")
	want := []helpLine{{"A", true}, {"body one", false}, {"", false}, {"body two", false}}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestWrapHelp(t *testing.T) {
	in := []helpLine{{"Head", true}, {"aaa bbb ccc ddd", false}, {"", false}}
	got := wrapHelp(in, 7)
	if len(got) != 4 || got[1].Text != "aaa bbb" || got[2].Text != "ccc ddd" || got[3].Text != "" || !got[0].Heading || got[1].Heading {
		t.Fatalf("got %+v", got)
	}
}

func TestHelpOpenClose(t *testing.T) {
	g := testGame(t)
	g.settingsOpen = true
	g.activateSetting(rowHelp)
	if !g.helpOpen || g.helpScroll != 0 {
		t.Fatalf("not open: %v %d", g.helpOpen, g.helpScroll)
	}
	g.buildHits()
	lh := g.lineH()
	h, ok := g.hits.At(connectX+g.railW()+5, connectFieldsTop(lh)+1)
	if !ok || h.Kind != state.HitHelpBack {
		t.Fatalf("back hit = %+v %v", h, ok)
	}
	g.scrollHelp(5)
	g.helpBack()
	if g.helpOpen || g.helpScroll != 0 || !g.settingsOpen {
		t.Fatalf("after back: open=%v scroll=%d settings=%v", g.helpOpen, g.helpScroll, g.settingsOpen)
	}
}

func TestHelpScrollClamps(t *testing.T) {
	g := testGame(t)
	g.openHelp()
	max := g.helpMax()
	if max <= 0 {
		t.Fatalf("help should overflow the window, max=%d", max)
	}
	g.scrollHelp(-50)
	if g.helpScroll != 0 {
		t.Fatalf("top clamp: %d", g.helpScroll)
	}
	g.scrollHelp(3)
	if g.helpScroll != 3 {
		t.Fatalf("scroll: %d", g.helpScroll)
	}
	g.scrollHelp(100000)
	if g.helpScroll != max {
		t.Fatalf("bottom clamp: %d, want %d", g.helpScroll, max)
	}
	g.h = 100000 // everything fits: nothing to scroll
	g.scrollHelp(5)
	if g.helpScroll != 0 {
		t.Fatalf("no overflow: %d", g.helpScroll)
	}
}

func TestHelpRowIsLast(t *testing.T) {
	if rowHelp != settingsRows-1 {
		t.Fatalf("rowHelp=%d settingsRows=%d", rowHelp, settingsRows)
	}
}

func TestUpdateCheckIgnoresOldSetting(t *testing.T) {
	// update_check = false is kept so old files load, but the check still runs.
	off := false
	g := testGame(t)
	g.set.UpdateCheck = &off
	g.ver = "v1.0.0"
	g.updateCh = make(chan updateResult, 1)
	called := make(chan struct{}, 1)
	g.checkFn = func(context.Context, string) (update.Available, bool, error) {
		called <- struct{}{}
		return update.Available{}, false, nil
	}
	g.startUpdateCheck()
	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("check did not run with update_check = false")
	}
}

func TestHelpScrollReclampsBeforeDelta(t *testing.T) {
	g := testGame(t)
	g.openHelp()
	max := g.helpMax()
	g.helpScroll = max + 15 // content shrank (resize or scale change)
	g.scrollHelp(-1)
	if g.helpScroll != max-1 {
		t.Fatalf("scroll = %d, want %d", g.helpScroll, max-1)
	}
}
