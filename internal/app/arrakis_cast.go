package app

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

// faction is whose colors a figure of the desert wears.
type faction int

const (
	fremen faction = iota
	beneGesserit
	atreides
	corrino
	harkonnen
	guild
	tleilaxu
	ixian
	honoredMatres
)

// factionLook is how a faction shows: its name, the color of its trim, the
// color of its eyes, whether its people go hooded or caped, and whether
// they bear a blade.
type factionLook struct {
	name   string
	trim   ui.Color
	eyes   ui.Color
	hooded bool
	armed  bool // a blade at the hip
}

var factionLooks = map[faction]factionLook{
	fremen:        {"Fremen", ui.RGB(0x5f, 0xb8, 0xff), ui.RGB(0x5f, 0xb8, 0xff), true, false},
	beneGesserit:  {"Bene Gesserit", ui.RGB(0xa9, 0x8b, 0xf0), ui.RGB(0x7c, 0xc4, 0xff), true, false},
	atreides:      {"House Atreides", ui.RGB(0x5f, 0xc0, 0x86), ui.RGB(0xe8, 0xdc, 0xc0), false, true},
	corrino:       {"House Corrino", ui.RGB(0xe6, 0xc1, 0x5a), ui.RGB(0xe8, 0xdc, 0xc0), false, false},
	harkonnen:     {"House Harkonnen", ui.RGB(0xd8, 0x56, 0x4a), ui.RGB(0xe8, 0xdc, 0xc0), false, true},
	guild:         {"Spacing Guild", ui.RGB(0xf0, 0x8c, 0x3c), ui.RGB(0x5f, 0xb8, 0xff), false, false},
	tleilaxu:      {"Bene Tleilax", ui.RGB(0x7f, 0xb5, 0xa8), ui.RGB(0xe8, 0xdc, 0xc0), true, false},
	ixian:         {"Ix", ui.RGB(0x9f, 0xb4, 0xc8), ui.RGB(0xe8, 0xdc, 0xc0), false, false},
	honoredMatres: {"Honored Matres", ui.RGB(0xf0, 0x6a, 0x3a), ui.RGB(0xff, 0x9a, 0x4a), true, false},
}

// castMember is a person of the books: what they are called there and whom
// they serve.
type castMember struct {
	title   string
	faction faction
}

// cast names the people of the six books an agent may be called after. A
// name the cast does not know is a Fremen of the deep desert.
var cast = map[string]castMember{
	"odrade":    {"Mother Superior", beneGesserit},
	"taraza":    {"Mother Superior", beneGesserit},
	"bellonda":  {"Mentat of the Archives", beneGesserit},
	"tamalane":  {"Reverend Mother of the Council", beneGesserit},
	"lucilla":   {"Imprinter", beneGesserit},
	"schwangyu": {"Reverend Mother of the Keep", beneGesserit},
	"dortujla":  {"Reverend Mother of the Marches", beneGesserit},
	"garimi":    {"Proctor", beneGesserit},
	"jessica":   {"Lady of the Atreides", beneGesserit},
	"mohiam":    {"Truthsayer", beneGesserit},
	"anirul":    {"Kwisatz Mother", beneGesserit},
	"sheeana":   {"Rider of worms", fremen},
	"paul":      {"Muad'Dib", fremen},
	"muad'dib":  {"Muad'Dib", fremen},
	"usul":      {"Muad'Dib", fremen},
	"chani":     {"Sayyadina", fremen},
	"stilgar":   {"Naib of Sietch Tabr", fremen},
	"kynes":     {"Planetologist", fremen},
	"liet":      {"Planetologist", fremen},
	"harah":     {"Keeper of the sietch", fremen},
	"jamis":     {"Fedaykin", fremen},
	"otheym":    {"Fedaykin", fremen},
	"duncan":    {"Swordmaster", atreides},
	"idaho":     {"Swordmaster", atreides},
	"teg":       {"Bashar", atreides},
	"miles":     {"Bashar", atreides},
	"burzmali":  {"Bashar", atreides},
	"gurney":    {"Warmaster", atreides},
	"thufir":    {"Mentat", atreides},
	"hawat":     {"Mentat", atreides},
	"leto":      {"God Emperor", atreides},
	"ghanima":   {"Heir of the Atreides", atreides},
	"alia":      {"Regent", atreides},
	"moneo":     {"Majordomo", atreides},
	"siona":     {"Rebel of the Golden Path", atreides},
	"nayla":     {"Fish Speaker", atreides},
	"yueh":      {"Suk doctor", atreides},
	"irulan":    {"Princess and chronicler", corrino},
	"shaddam":   {"Padishah Emperor", corrino},
	"wensicia":  {"Princess in exile", corrino},
	"farad'n":   {"Royal scribe", corrino},
	"vladimir":  {"Baron", harkonnen},
	"feyd":      {"na-Baron", harkonnen},
	"rabban":    {"Governor", harkonnen},
	"piter":     {"Twisted Mentat", harkonnen},
	"edric":     {"Navigator", guild},
	"scytale":   {"Face Dancer", tleilaxu},
	"waff":      {"Master of masters", tleilaxu},
	"bijaz":     {"Dwarf of the Tleilaxu", tleilaxu},
	"hwi":       {"Ambassador of Ix", ixian},
	"murbella":  {"Mother Commander", honoredMatres},
	"dama":      {"Great Honored Matre", honoredMatres},
	"logno":     {"Honored Matre", honoredMatres},
}

// castOf finds the person of the books in names: an agent's name or ID, or
// the words before a colon in a session's title. A word of a name is
// enough, so "Miles Teg" and "Duncan Idaho: CI fixes" are found.
func castOf(names ...string) (castMember, bool) {
	for _, name := range names {
		name, _, _ = strings.Cut(name, ":")
		for _, word := range strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
			return r == ' ' || r == '-' || r == '_' || r == ','
		}) {
			if who, ok := cast[word]; ok {
				return who, true
			}
		}
	}
	return castMember{}, false
}
