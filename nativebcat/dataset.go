// SPDX-License-Identifier: AGPL-3.0-only
package nativebcat

import (
	"errors"
	"regexp"
	"strings"
	"time"
)

// ErrNotFound: the news service has no current dataset for the title.
var ErrNotFound = errors.New("not found")

// Dataset is one title's sealed dataset as the news service's internal API
// (GET /internal/datasets/<title>) describes it. Key names the object to fetch.
type Dataset struct {
	TitleID    string        `json:"title_id"`
	ValidFrom  time.Time     `json:"valid_from"`
	ValidUntil *time.Time    `json:"valid_until"`
	Files      []DatasetFile `json:"files"`
}

type DatasetFile struct {
	Path   string `json:"path"`
	Key    string `json:"key"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

var reSwitchTID = regexp.MustCompile(`^[0-9a-f]{16}$`)

// ValidateSwitchTitleID: 16 lowercase hex chars.
func ValidateSwitchTitleID(s string) error {
	if !reSwitchTID.MatchString(s) {
		return errors.New("switch title id must be 16 lowercase hex chars")
	}
	return nil
}

// ValidateFilePath rejects unsafe dataset destination paths.
func ValidateFilePath(p string) error {
	if p == "" || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\\x00") || len(p) > 512 {
		return errors.New("unsafe dataset path")
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return errors.New("dataset path has an empty/dot segment")
		}
	}
	return nil
}
