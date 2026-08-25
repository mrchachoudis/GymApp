// Package exercises is the exercise library: 1,318 movements with their
// equipment, the muscles they train, and how to perform them.
//
// It is the single source of exercise data in the app. internal/muscle reads it
// for the volume mapping, the API serves it for search and browse, and the
// phone renders it. Two copies of this data would drift, and a drift between
// "what the muscle map credits" and "what the library says a lift trains" is
// the kind of bug nobody notices for months.
//
// # Provenance
//
// Derived from hasaneyldrm/exercises-dataset, by way of the openGym project
// which slimmed and normalized it. That dataset is NOT under openGym's AGPL:
// openGym's own NOTICE.md records it as Creative Commons under the upstream
// dataset's terms, and it is used here on that basis.
//
// The demo images and animations are deliberately NOT vendored. They are 139 MB
// and the upstream notice asks that anyone redistributing them review the
// licence first, so this repository stores only the filenames and serves the
// files from a directory the operator populates. See scripts/fetch-media.sh.
//
// No openGym application code is used anywhere. It is AGPL-3.0 and copying it
// would relicense this repository.
package exercises

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/json"
	"io"
	"sort"
	"strings"
	"sync"
)

// Exercise is one library movement.
type Exercise struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	BodyPart  string   `json:"body_part"`
	Equipment string   `json:"equipment"`
	Target    string   `json:"target"`
	Secondary []string `json:"secondary"`
	// Steps is the English instruction list, five or six lines typically.
	Steps []string `json:"steps,omitempty"`
	// Image and Animation are filenames, not URLs. The client builds a URL
	// against the service's media route, so moving where media lives does not
	// invalidate stored data.
	Image     string `json:"image,omitempty"`
	Animation string `json:"animation,omitempty"`
}

// wire mirrors the compact on-disk keys. The stored form is terse because it is
// embedded in the binary; the exported form is readable because it is an API.
type wire struct {
	ID        string   `json:"id"`
	Name      string   `json:"n"`
	BodyPart  string   `json:"b"`
	Equipment string   `json:"e"`
	Target    string   `json:"t"`
	Secondary []string `json:"s"`
	Steps     []string `json:"i"`
	Image     string   `json:"img"`
	Animation string   `json:"gif"`
}

// The library is embedded gzipped: 837 KB of JSON compresses to 111 KB, and it
// is inflated once on first use rather than on every start, since the CLI paths
// that only print a rank never touch it.
//
//go:embed library.json.gz
var libraryGz []byte

var (
	once    sync.Once
	all     []Exercise
	byID    map[string]*Exercise
	byName  map[string][]*Exercise
	loadErr error
)

func load() {
	once.Do(func() {
		zr, err := gzip.NewReader(bytes.NewReader(libraryGz))
		if err != nil {
			loadErr = err
			return
		}
		defer zr.Close()
		raw, err := io.ReadAll(zr)
		if err != nil {
			loadErr = err
			return
		}
		var ws []wire
		if err := json.Unmarshal(raw, &ws); err != nil {
			loadErr = err
			return
		}

		all = make([]Exercise, len(ws))
		byID = make(map[string]*Exercise, len(ws))
		byName = make(map[string][]*Exercise, len(ws))
		for i, w := range ws {
			all[i] = Exercise{
				ID: w.ID, Name: w.Name, BodyPart: w.BodyPart, Equipment: w.Equipment,
				Target: w.Target, Secondary: w.Secondary, Steps: w.Steps,
				Image: w.Image, Animation: w.Animation,
			}
			e := &all[i]
			byID[e.ID] = e
			k := Normalize(e.Name)
			byName[k] = append(byName[k], e)
		}
	})
}

// All returns the whole library. The slice is shared, so callers must not
// mutate it.
func All() []Exercise {
	load()
	return all
}

// Err reports a failure to inflate or parse the embedded library. It is a build
// problem wearing a runtime costume, so callers generally log it and carry on
// with an empty library rather than refusing to start.
func Err() error {
	load()
	return loadErr
}

// ByID returns one exercise.
func ByID(id string) (Exercise, bool) {
	load()
	e, ok := byID[id]
	if !ok {
		return Exercise{}, false
	}
	return *e, true
}

// ByName returns every entry sharing a normalized name, which is how the same
// movement appears once per piece of equipment.
func ByName(name string) []*Exercise {
	load()
	return byName[Normalize(name)]
}

// Normalize reduces a name to comparable form: lowercase, alphanumeric, single
// spaces. Hyphens, slashes and parentheses all collapse, so "chest-supported
// row" and "chest supported row" are the same key.
//
// It lives here rather than in internal/muscle because both that package and
// the search below have to agree on it exactly.
func Normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevSpace = false
		default:
			if !prevSpace {
				b.WriteByte(' ')
			}
			prevSpace = true
		}
	}
	return strings.TrimSpace(b.String())
}

// Query filters the library.
type Query struct {
	// Text matches on name. Every whitespace-separated term must appear, so
	// "incline db" narrows rather than widens.
	Text string
	// BodyPart, Equipment and Target are exact filters, empty meaning any.
	BodyPart  string
	Equipment string
	Target    string
	Limit     int
	Offset    int
}

// Result is a page of matches plus the unpaged total, so the client can say
// "showing 50 of 214" instead of guessing whether more exist.
type Result struct {
	Total     int        `json:"total"`
	Offset    int        `json:"offset"`
	Exercises []Exercise `json:"exercises"`
}

const defaultLimit = 50

// equipmentRank orders equipment canonical-first, so a query that matches many
// variants of one movement leads with the version most people mean.
//
// Anything unlisted sorts last, which puts the bosu balls and wheel rollers
// behind the barbell without having to enumerate all twenty-eight values.
var equipmentRank = map[string]int{
	"barbell": 0, "dumbbell": 1, "body weight": 2, "weighted": 3,
	"cable": 4, "leverage machine": 5, "smith machine": 6, "ez barbell": 7,
	"kettlebell": 8, "band": 9,
}

func equipmentOrder(e string) int {
	if n, ok := equipmentRank[e]; ok {
		return n
	}
	return len(equipmentRank)
}

// Search runs the query.
//
// Ranking matters more than it looks. The dataset names movements
// equipment-first -- "barbell bench press", "cable bench press" -- so for a
// query like "bench press" the strongest signal that an entry IS that movement
// is that its name ENDS with the query. Ranking on a prefix instead returns
// "squat jerk" and "squat on bosu ball" ahead of the barbell squat, and
// "band bench press" ahead of the barbell bench press, which is what makes a
// library of this size feel broken.
//
// So: exact name, then suffix, then prefix, then contains; and within a tier,
// canonical equipment first and shorter names before longer.
func Search(q Query) Result {
	load()

	terms := strings.Fields(Normalize(q.Text))
	var hits []*Exercise
	for i := range all {
		e := &all[i]
		if q.BodyPart != "" && !strings.EqualFold(e.BodyPart, q.BodyPart) {
			continue
		}
		if q.Equipment != "" && !strings.EqualFold(e.Equipment, q.Equipment) {
			continue
		}
		if q.Target != "" && !strings.EqualFold(e.Target, q.Target) {
			continue
		}
		if len(terms) > 0 {
			name := " " + Normalize(e.Name) + " "
			matched := true
			for _, t := range terms {
				if !strings.Contains(name, t) {
					matched = false
					break
				}
			}
			if !matched {
				continue
			}
		}
		hits = append(hits, e)
	}

	key := Normalize(q.Text)
	rank := func(e *Exercise) int {
		n := Normalize(e.Name)
		switch {
		case key == "":
			return 3
		case n == key:
			return 0
		case strings.HasSuffix(n, " "+key):
			return 1
		case strings.HasPrefix(n, key+" "):
			return 2
		default:
			return 3
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		ri, rj := rank(hits[i]), rank(hits[j])
		if ri != rj {
			return ri < rj
		}
		ei, ej := equipmentOrder(hits[i].Equipment), equipmentOrder(hits[j].Equipment)
		if ei != ej {
			return ei < ej
		}
		if len(hits[i].Name) != len(hits[j].Name) {
			return len(hits[i].Name) < len(hits[j].Name)
		}
		return hits[i].Name < hits[j].Name
	})

	res := Result{Total: len(hits), Offset: q.Offset}
	limit := q.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	if q.Offset >= len(hits) {
		res.Exercises = []Exercise{}
		return res
	}
	end := q.Offset + limit
	if end > len(hits) {
		end = len(hits)
	}
	for _, e := range hits[q.Offset:end] {
		res.Exercises = append(res.Exercises, *e)
	}
	return res
}

// Facets are the distinct filter values, each with a count. The phone builds
// its filter chips from these rather than hardcoding a list that would rot the
// next time the dataset is refreshed.
type Facets struct {
	BodyParts []Facet `json:"body_parts"`
	Equipment []Facet `json:"equipment"`
	Targets   []Facet `json:"targets"`
}

type Facet struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

func AllFacets() Facets {
	load()
	bp, eq, tg := map[string]int{}, map[string]int{}, map[string]int{}
	for _, e := range all {
		bp[e.BodyPart]++
		eq[e.Equipment]++
		tg[e.Target]++
	}
	return Facets{BodyParts: facetList(bp), Equipment: facetList(eq), Targets: facetList(tg)}
}

func facetList(m map[string]int) []Facet {
	out := make([]Facet, 0, len(m))
	for v, n := range m {
		out = append(out, Facet{Value: v, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Value < out[j].Value
	})
	return out
}
