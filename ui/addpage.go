package ui

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/text/v2"

	"github.com/medeirosvictor/hermec/discover"
	"github.com/medeirosvictor/hermec/ui/state"
)

// addMode is which step of the Add a Server page is showing.
type addMode int

const (
	addChoose addMode = iota // Create my own / Join a server
	addCreate                // the create form
	addJoin                  // the connect form
)

// firewallLine is the Windows Firewall hint shown on the create form and in
// the help text.
const firewallLine = "Windows will ask to allow HERMEC on your network — choose Allow so friends can connect."

const (
	defaultHostPort = 7697
	maxPortDigits   = 5
	portRangeMsg    = "port must be 1–65535"
	nameRequiredMsg = "server name is required"
	passMask        = '•'
)

// createForm is the input state of the create page.
type createForm struct {
	name, pass, port field
	focus            int // 0 name, 1 password, 2 port
	err              string
}

func newCreateForm() createForm {
	return createForm{port: field{runes: []rune(strconv.Itoa(defaultHostPort))}}
}

func (f *createForm) focused() *field {
	switch f.focus {
	case 1:
		return &f.pass
	case 2:
		return &f.port
	}
	return &f.name
}

// typed appends printable runes to the focused field. The port takes digits
// only, up to five.
func (f *createForm) typed(rs []rune) {
	cur := f.focused()
	for _, r := range rs {
		switch {
		case f.focus == 2:
			if r >= '0' && r <= '9' && len(cur.runes) < maxPortDigits {
				cur.runes = append(cur.runes, r)
			}
		case r >= ' ' && r != 0x7f && len(cur.runes) < maxFieldRunes:
			cur.runes = append(cur.runes, r)
		}
	}
}

// validate checks the form; err is the user-facing reason it cannot submit.
func (f *createForm) validate() (name, pass string, port int, err string) {
	name = strings.TrimSpace(f.name.String())
	if name == "" {
		return "", "", 0, nameRequiredMsg
	}
	port, e := strconv.Atoi(f.port.String())
	if e != nil || port < 1 || port > 65535 {
		return "", "", 0, portRangeMsg
	}
	return name, f.pass.String(), port, ""
}

// submitCreate validates the form and creates the hosted server. A failure
// leaves the form up with the reason in err.
func (g *game) submitCreate() {
	if g.hosted != nil {
		g.cf.err = "already hosting " + g.hosted.cfg.ServerName
		return
	}
	name, pass, port, msg := g.cf.validate()
	if msg != "" {
		g.cf.err = msg
		return
	}
	if err := g.create(name, pass, port); err != nil {
		g.cf.err = err.Error()
		return
	}
	g.cf = newCreateForm()
	g.view(hostedKey)
}

// addPageShown reports that the choice or create page replaces the connect
// form: no session is viewed and the join step is not the one open.
func (g *game) addPageShown() bool {
	return g.curKey == "" && g.add != addJoin
}

// createChoiceHint is the one-line hint under the Create my own choice.
func (g *game) createChoiceHint() string {
	if g.hosted != nil {
		return "already hosting " + g.hosted.cfg.ServerName
	}
	return "host a server on this computer"
}

// chooseAdd activates choice i: 0 create, 1 join. Create does nothing while
// a server is already hosted, so hosted.toml is never overwritten.
func (g *game) chooseAdd(i int) {
	switch i {
	case 0:
		if g.hosted != nil {
			return
		}
		g.add = addCreate
		g.cf = newCreateForm()
		g.startAddInvite()
	case 1:
		g.add = addJoin
		g.connect.sel = -1
		g.connect.focus = 1
	}
}

// addEsc steps back on a pressed Esc: create and join return to the choices,
// the choices return to the server that was viewed before "+", if any.
func (g *game) addEsc(pressed bool) {
	if !pressed || g.escSpent {
		return
	}
	switch g.add {
	case addCreate, addJoin:
		if g.add == addJoin && g.connect.local {
			return // the -local connect form has no choices to return to
		}
		g.add = addChoose
		g.escSpent = true
	default:
		if p := g.addPrev; p != "" && g.sess[p] != nil {
			g.escSpent = true
			g.view(p)
		}
	}
}

// startAddInvite looks up this machine's addresses once, off the frame.
func (g *game) startAddInvite() {
	self := g.selfAddrs
	if self == nil {
		self = discover.SelfAddrs
	}
	ch := make(chan inviteResult, 1)
	g.addInvCh, g.addInviteDone = ch, false
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		ts, lan := self(ctx)
		ch <- inviteResult{ts: ts, lan: lan}
	}()
}

// pumpAddInvite stores a finished lookup for the create page to show.
func (g *game) pumpAddInvite() {
	select {
	case r := <-g.addInvCh:
		g.addTS, g.addLAN, g.addInviteDone = r.ts, r.lan, true
	default:
	}
}

// createInviteText is the invite line under the Create button.
func (g *game) createInviteText() string {
	if !g.addInviteDone {
		return "invite: looking up your address..."
	}
	port, err := strconv.Atoi(g.cf.port.String())
	if err != nil || port < 1 || port > 65535 {
		port = defaultHostPort
	}
	return "invite: " + inviteAddress(g.addTS, g.addLAN, port)
}

// Layout of the add pages. Hit rects and drawing both use these.

func (g *game) addX() float64 { return connectX + g.railW() }

func (g *game) addTop() float64 { return connectFieldsTop(g.lineH()) + g.bannerH() }

// addChoiceRect is choice panel i, the two sit side by side.
func (g *game) addChoiceRect(i int) rectF {
	x0 := g.addX()
	avail := float64(g.w) - connectX - x0
	gap := float64(pad)
	w := (avail - gap) / 2
	if w > 320 {
		w = 320
	}
	if w < 40 {
		w = 40
	}
	x := x0 + float64(i)*(w+gap)
	y := g.addTop()
	return rectF{x, y, x + w, y + 5*g.lineH()}
}

// createRowY is the y of create row i: 0 name, 1 password, 2 port.
func (g *game) createRowY(i int) float64 { return g.addTop() + float64(i)*g.lineH() }

// createBtnRect is the Create button, below the firewall line.
func (g *game) createBtnRect() rectF {
	lh := g.lineH()
	x := g.addX()
	y := g.addTop() + (4+float64(len(g.firewallLines())))*lh
	w := text.Advance("Create", g.face) + 2*pad
	return rectF{x, y, x + w, y + lh + pad/2}
}

func (g *game) addAddHits() {
	switch {
	case g.curKey != "" || g.add == addJoin:
		g.addConnectHits()
	case g.add == addChoose:
		for i := 0; i < 2; i++ {
			r := g.addChoiceRect(i)
			g.hits.Add(state.Hit{X0: r.x0, Y0: r.y0, X1: r.x1, Y1: r.y1, Kind: state.HitAddChoice, ID: i})
		}
	case g.add == addCreate:
		lh := g.lineH()
		for i := 0; i < 3; i++ {
			y := g.createRowY(i)
			g.hits.Add(state.Hit{X0: g.addX(), Y0: y, X1: float64(g.w), Y1: y + lh, Kind: state.HitCreateField, ID: i})
		}
		r := g.createBtnRect()
		g.hits.Add(state.Hit{X0: r.x0, Y0: r.y0, X1: r.x1, Y1: r.y1, Kind: state.HitCreateBtn})
	}
}

// updateAdd handles input for the choice and create pages.
func (g *game) updateAdd() {
	click := inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft)
	var h state.Hit
	var ok bool
	if click {
		h, ok = g.hitAt()
	}
	switch g.add {
	case addChoose:
		if inpututil.IsKeyJustPressed(ebiten.KeyArrowLeft) || inpututil.IsKeyJustPressed(ebiten.KeyArrowUp) {
			g.addSel = 0
		}
		if inpututil.IsKeyJustPressed(ebiten.KeyArrowRight) || inpututil.IsKeyJustPressed(ebiten.KeyArrowDown) {
			g.addSel = 1
		}
		if ok && h.Kind == state.HitAddChoice {
			g.chooseAdd(h.ID)
		} else if enterPressed() {
			g.chooseAdd(g.addSel)
		}
	case addCreate:
		f := &g.cf
		if ok {
			switch h.Kind {
			case state.HitCreateField:
				f.focus = h.ID
			case state.HitCreateBtn:
				g.submitCreate()
				return
			}
		}
		if inpututil.IsKeyJustPressed(ebiten.KeyTab) {
			f.focus = (f.focus + 1) % 3
		}
		f.typed(ebiten.AppendInputChars(nil))
		cur := f.focused()
		if inpututil.IsKeyJustPressed(ebiten.KeyBackspace) {
			cur.backspace()
		} else if d := inpututil.KeyPressDuration(ebiten.KeyBackspace); d > 30 && d%3 == 0 {
			cur.backspace()
		}
		if enterPressed() {
			g.submitCreate()
		}
	}
}

func (g *game) drawAddPage(screen *ebiten.Image) {
	th, lh := g.th, g.lineH()
	g.drawContentPanel(screen, g.bannerH())
	x, y := g.addX(), connectY+g.bannerH()
	title := "Add a Server"
	if g.add == addCreate {
		title = "Create my own"
	}
	g.drawTextF(screen, g.faceT, title, x, y, th.Bright)

	if g.add == addChoose {
		hover := -1
		if h, ok := g.hitAt(); ok && h.Kind == state.HitAddChoice {
			hover = h.ID
		}
		labels := [2]string{"Create my own", "Join a server"}
		hints := [2]string{g.createChoiceHint(), "connect to a friend's server"}
		for i := 0; i < 2; i++ {
			r := g.addChoiceRect(i)
			t := 0.12
			if i == hover || i == g.addSel {
				t = 0.24
			}
			g.drawPanelFade(screen, r.x0, r.y0, r.x1, r.y1, mixRGBA(th.Panel, th.Bright, t), 4)
			col, hc := th.Bright, th.Dim
			if i == 0 && g.hosted != nil {
				col = th.Dim
			}
			g.drawText(screen, labels[i], r.x0+pad, r.y0+lh, col)
			g.drawTextF(screen, g.faceS, hints[i], r.x0+pad, r.y0+2.5*lh, hc)
		}
		if g.addPrev != "" && g.sess[g.addPrev] != nil {
			g.drawTextF(screen, g.faceS, "Esc to go back", x, g.addTop()+6*lh, th.Dim)
		}
		return
	}

	cursor := ""
	if (g.frame/30)%2 == 0 {
		cursor = "_"
	}
	f := &g.cf
	rows := [3]struct {
		label string
		val   string
	}{
		{"name:     ", f.name.String()},
		{"password: ", strings.Repeat(string(passMask), len(f.pass.runes))},
		{"port:     ", f.port.String()},
	}
	for i, r := range rows {
		val, marker := r.val, "  "
		if i == f.focus {
			val += cursor
			marker = "> "
		}
		g.drawText(screen, marker+r.label+val, x, g.createRowY(i), th.FG)
	}
	for i, l := range g.firewallLines() {
		g.drawText(screen, l, x, g.createRowY(3)+lh/2+float64(i)*lh, th.Dim)
	}
	b := g.createBtnRect()
	fill := mixRGBA(th.Panel, th.Bright, 0.18)
	if h, ok := g.hitAt(); ok && h.Kind == state.HitCreateBtn {
		fill = mixRGBA(th.Panel, th.Bright, 0.30)
	}
	g.drawPanelFade(screen, b.x0, b.y0, b.x1, b.y1, fill, 4)
	g.drawText(screen, "Create", b.x0+pad, b.y0+(b.y1-b.y0-lh)/2, th.Bright)
	y = b.y1 + lh/2
	if f.err != "" {
		g.drawText(screen, f.err, x, y, th.Warn)
		y += lh
	}
	g.drawTextF(screen, g.faceS, g.createInviteText(), x, y, th.Dim)
	g.drawTextF(screen, g.faceS, "Enter to create   Tab to switch field   Esc to go back", x, y+lh, th.Dim)
}

// initAdd sets the page a fresh launch opens on: the -local connect form, as
// before, or else the choices.
func (g *game) initAdd() {
	g.add = addChoose
	if g.connect.local {
		g.add = addJoin
	}
}

// addCols is how many monospace columns fit right of the add page's origin.
func (g *game) addCols() int {
	return max(int((float64(g.w)-g.addX()-connectX)/g.glyphW()), 10)
}

// firewallLines is the firewall hint wrapped to the page width.
func (g *game) firewallLines() []string { return state.Wrap(firewallLine, g.addCols()) }
