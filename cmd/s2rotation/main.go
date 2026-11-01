// Command s2rotation regenerates Splatoon 2's BCAT rotation files inside a bcatserve dataset
// directory (<root>/0100f8f0000a2000), using the files already there as the template, and
// rewrites manifest.json (sizes, hashes, validity). Restart bcatserve afterwards: datasets are
// read at start-up. See docs/splatoon2-rotation.md.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"nx-bcat/byml"
	"nx-bcat/s2rotation"
)

const titleID = "0100f8f0000a2000"

func readByml(path string) byml.Hash {
	b, err := os.ReadFile(path)
	if err != nil {
		log.Fatal(err)
	}
	v, err := byml.Decode(b)
	if err != nil {
		log.Fatalf("%s: %v", path, err)
	}
	h, ok := v.(byml.Hash)
	if !ok {
		log.Fatalf("%s: root is not a hash", path)
	}
	return h
}

func writeAtomic(path string, b []byte) {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		log.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		log.Fatal(err)
	}
}

func main() {
	root := flag.String("root", "", "bcatserve datasets root (contains "+titleID+"/)")
	days := flag.Int("days", 365, "how far ahead to generate")
	nowS := flag.String("now", "", "override the current time (RFC3339), for tests")
	dry := flag.Bool("n", false, "generate and report, write nothing")
	dump := flag.String("dump", "", "print a BYAML file as JSON and exit")
	flag.Parse()
	if *dump != "" {
		v, err := byml.Decode(mustRead(*dump))
		if err != nil {
			log.Fatal(err)
		}
		out, _ := json.MarshalIndent(v, "", " ")
		fmt.Println(string(out))
		return
	}
	if *root == "" {
		log.Fatal("-root is required")
	}
	now := time.Now().UTC()
	if *nowS != "" {
		t, err := time.Parse(time.RFC3339, *nowS)
		if err != nil {
			log.Fatal(err)
		}
		now = t.UTC()
	}
	horizon := time.Duration(*days) * 24 * time.Hour
	dir := filepath.Join(*root, titleID)
	files := filepath.Join(dir, "files")

	vs, err := s2rotation.VS(readByml(filepath.Join(files, "vsdata/VSSetting_0.byaml")), now, horizon)
	if err != nil {
		log.Fatal(err)
	}
	coop, err := s2rotation.Coop(readByml(filepath.Join(files, "coopdata/CoopSetting.byaml")), now, horizon)
	if err != nil {
		log.Fatal(err)
	}
	vsB, err := byml.Encode(vs)
	if err != nil {
		log.Fatal(err)
	}
	coopB, err := byml.Encode(coop)
	if err != nil {
		log.Fatal(err)
	}
	from, _ := s2rotation.VSStart(vs)
	until, _ := s2rotation.VSEnd(vs)
	cend, _ := s2rotation.CoopEnd(coop)
	log.Printf("vs %s .. %s (%d bytes), coop to %s (%d bytes)", from.Format(time.RFC3339), until.Format(time.RFC3339), len(vsB), cend.Format(time.RFC3339), len(coopB))
	if *dry {
		return
	}

	// Manifest: keep every entry, refresh the three rotation files, move the window.
	var m map[string]any
	if err := json.Unmarshal(mustRead(filepath.Join(dir, "manifest.json")), &m); err != nil {
		log.Fatal(err)
	}
	gen := map[string][]byte{"vsdata/VSSetting_0.byaml": vsB, "vsdata/VSSetting_1.byaml": vsB, "coopdata/CoopSetting.byaml": coopB}
	for rel, b := range gen {
		writeAtomic(filepath.Join(files, rel), b)
	}
	for _, f := range m["files"].([]any) {
		e := f.(map[string]any)
		if b, ok := gen[e["path"].(string)]; ok {
			sum := sha256.Sum256(b)
			e["size"], e["sha256"] = len(b), hex.EncodeToString(sum[:])
		}
	}
	m["valid_from"] = from.Format(time.RFC3339)
	m["valid_until"] = until.Format(time.RFC3339)
	out, _ := json.MarshalIndent(m, "", "  ")
	writeAtomic(filepath.Join(dir, "manifest.json"), append(out, '\n'))
}

func mustRead(p string) []byte {
	b, err := os.ReadFile(p)
	if err != nil {
		log.Fatal(err)
	}
	return b
}
