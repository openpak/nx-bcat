package s2rotation

import (
	"reflect"
	"testing"
	"time"

	"nx-bcat/byml"
)

func tmplVS() byml.Hash {
	return byml.Hash{"Version": int32(26), "Static": []any{int32(1)}, "Phases": []any{
		byml.Hash{"DateTime": "2026-10-31T00:00:00", "Regular": mode("cPnt", 1, 2), "Gachi": mode("cVar", 3, 4), "League": mode("cVgl", 5, 6)}}}
}

func tmplCoop() byml.Hash {
	var w []any
	for _, id := range []int32{0, 10, 20, 30, 40, 50} {
		w = append(w, id)
	}
	return byml.Hash{"Version": int32(26), "MonthlyRewardGears": []any{byml.Hash{"DateTime": "2026-11-01T00:00:00", "GearID": int32(21000), "GearKind": "cHead"}},
		"Phases": []any{byml.Hash{"StartDateTime": "2026-11-01T00:00:00", "EndDateTime": "2026-11-02T12:00:00", "StageID": int32(5000), "RareWeaponID": int32(20000), "WeaponSets": w[:4]},
			byml.Hash{"StartDateTime": "2026-11-02T18:00:00", "EndDateTime": "2026-11-04T06:00:00", "StageID": int32(5001), "RareWeaponID": int32(20000), "WeaponSets": w[2:]}}}
}

func TestVSDeterministicAndValid(t *testing.T) {
	now := time.Date(2026, 11, 3, 5, 0, 0, 0, time.UTC)
	a, err := VS(tmplVS(), now, 30*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := VS(tmplVS(), now.Add(48*time.Hour), 30*24*time.Hour)
	end, _ := VSEnd(a)
	if end.Before(now.Add(29 * 24 * time.Hour)) {
		t.Fatalf("ends %s", end)
	}
	// Regenerating later reproduces the overlap exactly.
	am := map[string]any{}
	for _, p := range a["Phases"].([]any) {
		am[p.(byml.Hash)["DateTime"].(string)] = p
	}
	for _, p := range b["Phases"].([]any) {
		if q, ok := am[p.(byml.Hash)["DateTime"].(string)]; ok && !reflect.DeepEqual(p, q) {
			t.Fatalf("phase changed between runs: %v vs %v", p, q)
		}
	}
	ph := a["Phases"].([]any)
	for i := 1; i < len(ph); i++ {
		x, y := ph[i].(byml.Hash), ph[i-1].(byml.Hash)
		for _, m := range []string{"Regular", "Gachi", "League"} {
			s, p := stagesOf(x[m]), stagesOf(y[m])
			if s[0] == s[1] || s[0] == p[0] || s[0] == p[1] || s[1] == p[0] || s[1] == p[1] || s[0] >= stageMax || s[1] >= stageMax {
				t.Fatalf("%s %v after %v", m, s, p)
			}
		}
		if x["Gachi"].(byml.Hash)["Rule"] == x["League"].(byml.Hash)["Rule"] {
			t.Fatal("same ranked rule")
		}
	}
	// Static keys survive, and the whole document encodes and decodes.
	if !reflect.DeepEqual(a["Static"], tmplVS()["Static"]) {
		t.Fatal("static key changed")
	}
	enc, err := byml.Encode(a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := byml.Decode(enc); err != nil {
		t.Fatal(err)
	}
}

func TestCoopOnlyAppends(t *testing.T) {
	now := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	c, err := Coop(tmplCoop(), now, 60*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ph, g := c["Phases"].([]any), c["MonthlyRewardGears"].([]any)
	if len(ph) != len(g)+1 || len(ph) < 20 || !reflect.DeepEqual(ph[:2], tmplCoop()["Phases"]) {
		t.Fatalf("phases %d gears %d", len(ph), len(g))
	}
	end, _ := CoopEnd(c)
	if end.Before(now.Add(55 * 24 * time.Hour)) {
		t.Fatalf("ends %s", end)
	}
}
