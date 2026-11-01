// Package s2rotation generates Splatoon 2's BCAT rotation files: vsdata/VSSetting_{0,1}.byaml
// (Regular, Anarchy and League phases, every two hours) and coopdata/CoopSetting.byaml (Salmon
// Run shifts and monthly gear).
//
// Both files are mostly static game configuration (stage first-appearance dates, weapon unlocks,
// matchmaking parameters) around one list of phases. The generator therefore works on a TEMPLATE:
// the file already deployed. It keeps every static key byte-for-byte as decoded and replaces only
// the phase lists, so no game configuration is stored in this repository and nothing about it is
// invented. The pools it draws from (weapon sets, gear ids) are read from the template as well.
//
// The output is deterministic: the VS chain restarts from a fixed epoch and each phase is seeded
// by its own start time, so regenerating later reproduces the same phases that were served before
// and only extends the horizon. Coop continues from the template's last shift the same way.
//
// ponytail: the game's own phase generator (LoopSeeds, PhasesSeeds in VSSetting) is not
// reimplemented; rotations are explicit phase lists, which is all the client needs.
package s2rotation

import (
	"fmt"
	"math/rand"
	"sort"
	"time"

	"nx-bcat/byml"
)

const (
	vsPhase  = 2 * time.Hour
	stageMax = 23 // stage ids 0..22
	timeFmt  = "2006-01-02T15:04:05"
)

// VSEpoch is where the generated VS chain starts; template phases before it are kept as they are.
var VSEpoch = time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)

var ranked = []string{"cVar", "cVlf", "cVgl", "cVcl"} // Splat Zones, Tower Control, Rainmaker, Clam Blitz

func fmtT(t time.Time) string { return t.UTC().Format(timeFmt) }

func parseT(s string) (time.Time, error) { return time.ParseInLocation(timeFmt, s, time.UTC) }

func hash(v any, key string) (byml.Hash, error) {
	h, ok := v.(byml.Hash)
	if !ok {
		return nil, fmt.Errorf("%s: not a hash", key)
	}
	return h, nil
}

func list(h byml.Hash, key string) ([]any, error) {
	l, ok := h[key].([]any)
	if !ok {
		return nil, fmt.Errorf("template has no %q list", key)
	}
	return l, nil
}

func mode(rule string, a, b int32) byml.Hash {
	return byml.Hash{"MapID0": a, "MapID1": b, "Rule": rule}
}

// pair picks two distinct stages, neither in the previous pair.
func pair(rng *rand.Rand, prev [2]int32) [2]int32 {
	for {
		a, b := int32(rng.Intn(stageMax)), int32(rng.Intn(stageMax))
		if a != b && a != prev[0] && a != prev[1] && b != prev[0] && b != prev[1] {
			return [2]int32{a, b}
		}
	}
}

func stagesOf(m any) [2]int32 {
	h, _ := m.(byml.Hash)
	a, _ := h["MapID0"].(int32)
	b, _ := h["MapID1"].(int32)
	return [2]int32{a, b}
}

// VS rebuilds the Phases of a VSSetting document: template phases before VSEpoch, then the
// generated chain to now+horizon, trimmed to start no earlier than 24 h before now.
func VS(tmpl byml.Hash, now time.Time, horizon time.Duration) (byml.Hash, error) {
	old, err := list(tmpl, "Phases")
	if err != nil {
		return nil, err
	}
	var phases []any
	var prev [3][2]int32 // regular, gachi, league
	for _, p := range old {
		ph, err := hash(p, "Phases[]")
		if err != nil {
			return nil, err
		}
		s, _ := ph["DateTime"].(string)
		t, err := parseT(s)
		if err != nil {
			return nil, fmt.Errorf("template phase time %q: %w", s, err)
		}
		if !t.Before(VSEpoch) {
			break
		}
		phases = append(phases, ph)
		prev = [3][2]int32{stagesOf(ph["Regular"]), stagesOf(ph["Gachi"]), stagesOf(ph["League"])}
	}
	end := now.UTC().Add(horizon).Truncate(vsPhase)
	for t := VSEpoch; t.Before(end); t = t.Add(vsPhase) {
		rng := rand.New(rand.NewSource(t.Unix()))
		g := ranked[rng.Intn(4)]
		l := ranked[rng.Intn(4)]
		for l == g {
			l = ranked[rng.Intn(4)]
		}
		prev[0], prev[1], prev[2] = pair(rng, prev[0]), pair(rng, prev[1]), pair(rng, prev[2])
		phases = append(phases, byml.Hash{
			"DateTime": fmtT(t),
			"Regular":  mode("cPnt", prev[0][0], prev[0][1]),
			"Gachi":    mode(g, prev[1][0], prev[1][1]),
			"League":   mode(l, prev[2][0], prev[2][1]),
		})
	}
	// Trim the past, but never to nothing.
	cut := now.UTC().Truncate(vsPhase).Add(-24 * time.Hour)
	i := sort.Search(len(phases), func(i int) bool {
		t, _ := parseT(phases[i].(byml.Hash)["DateTime"].(string))
		return !t.Before(cut)
	})
	if i >= len(phases) {
		return nil, fmt.Errorf("no phase left after trimming")
	}
	out := byml.Hash{}
	for k, v := range tmpl {
		out[k] = v
	}
	out["Phases"] = phases[i:]
	return out, nil
}

// VSEnd is when the last phase of a VSSetting document ends.
func VSEnd(doc byml.Hash) (time.Time, error) {
	ph, err := list(doc, "Phases")
	if err != nil || len(ph) == 0 {
		return time.Time{}, fmt.Errorf("no phases")
	}
	s, _ := ph[len(ph)-1].(byml.Hash)["DateTime"].(string)
	t, err := parseT(s)
	return t.Add(vsPhase), err
}

// VSStart is the first phase's start.
func VSStart(doc byml.Hash) (time.Time, error) {
	ph, err := list(doc, "Phases")
	if err != nil || len(ph) == 0 {
		return time.Time{}, fmt.Errorf("no phases")
	}
	s, _ := ph[0].(byml.Hash)["DateTime"].(string)
	return parseT(s)
}

// Coop extends the shifts (and the monthly reward gear, one per shift) until they reach
// now+horizon. Existing shifts are never touched, so a regeneration only appends.
func Coop(tmpl byml.Hash, now time.Time, horizon time.Duration) (byml.Hash, error) {
	phases, err := list(tmpl, "Phases")
	if err != nil || len(phases) == 0 {
		return nil, fmt.Errorf("template has no coop phases")
	}
	gears, err := list(tmpl, "MonthlyRewardGears")
	if err != nil {
		return nil, err
	}
	// Pools come from the template: weapon sets seen, stages seen, gear ids per kind.
	weapons, stages, rare := map[int32]bool{}, map[int32]bool{}, map[int32]bool{}
	for _, p := range phases {
		h, _ := p.(byml.Hash)
		if s, ok := h["StageID"].(int32); ok {
			stages[s] = true
		}
		if r, ok := h["RareWeaponID"].(int32); ok {
			rare[r] = true
		}
		ws, _ := h["WeaponSets"].([]any)
		for _, w := range ws {
			if id, ok := w.(int32); ok && id >= 0 {
				weapons[id] = true
			}
		}
	}
	gearIDs := map[int32]bool{}
	for _, g := range gears {
		h, _ := g.(byml.Hash)
		if id, ok := h["GearID"].(int32); ok {
			gearIDs[id] = true
		}
	}
	sw, ss, sr, sg := keys(weapons), keys(stages), keys(rare), keys(gearIDs)
	if len(sw) < 4 || len(ss) < 2 || len(sg) == 0 {
		return nil, fmt.Errorf("template pools too small (weapons %d, stages %d, gear %d)", len(sw), len(ss), len(sg))
	}
	kinds := []string{"cHead", "cClothes", "cShoes"}

	last, _ := phases[len(phases)-1].(byml.Hash)
	start, err := parseT(last["StartDateTime"].(string))
	if err != nil {
		return nil, err
	}
	prevStage, _ := last["StageID"].(int32)
	until := now.UTC().Add(horizon)
	out := byml.Hash{}
	for k, v := range tmpl {
		out[k] = v
	}
	for {
		rng := rand.New(rand.NewSource(start.Unix()))
		gap := 42 * time.Hour
		if rng.Intn(4) == 0 {
			gap = 48 * time.Hour
		}
		next := start.Add(gap)
		if !next.Before(until) {
			break
		}
		st := ss[rng.Intn(len(ss))]
		for st == prevStage && len(ss) > 1 {
			st = ss[rng.Intn(len(ss))]
		}
		prevStage = st
		rw := int32(20000)
		if len(sr) > 1 && rng.Intn(12) == 0 {
			rw = sr[rng.Intn(len(sr))]
		}
		var set []any
		if rng.Intn(20) == 0 {
			set = []any{int32(-1), int32(-1), int32(-1), int32(-1)}
		} else {
			for _, i := range rng.Perm(len(sw))[:4] {
				set = append(set, sw[i])
			}
		}
		// A shift lasts its own following gap minus six hours (what the template does); that gap
		// is decided by the seed the next iteration will use, so compute it here from the same seed.
		nrng := rand.New(rand.NewSource(next.Unix()))
		ngap := 42 * time.Hour
		if nrng.Intn(4) == 0 {
			ngap = 48 * time.Hour
		}
		phases = append(phases, byml.Hash{
			"StartDateTime": fmtT(next), "EndDateTime": fmtT(next.Add(ngap - 6*time.Hour)),
			"StageID": st, "RareWeaponID": rw, "WeaponSets": set,
		})
		gears = append(gears, byml.Hash{"DateTime": fmtT(next), "GearID": sg[rng.Intn(len(sg))], "GearKind": kinds[rng.Intn(len(kinds))]})
		start = next
	}
	out["Phases"], out["MonthlyRewardGears"] = phases, gears
	return out, nil
}

// CoopEnd is when the last shift ends.
func CoopEnd(doc byml.Hash) (time.Time, error) {
	ph, err := list(doc, "Phases")
	if err != nil || len(ph) == 0 {
		return time.Time{}, fmt.Errorf("no phases")
	}
	return parseT(ph[len(ph)-1].(byml.Hash)["EndDateTime"].(string))
}

func keys(m map[int32]bool) []int32 {
	out := make([]int32, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
