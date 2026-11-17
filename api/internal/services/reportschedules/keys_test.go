package reportschedules

import (
	"bytes"
	"errors"
)

// testKeys is the signing key set every test in the package shares; it lives
// outside the integration files so the unit-tagged tests compile without them.
type testKeys struct{}

var testKey = bytes.Repeat([]byte{7}, 32)

func (testKeys) CurrentKey() ([]byte, int, error) { return testKey, 1, nil }
func (testKeys) KeyForVersion(v int) ([]byte, error) {
	if v != 1 {
		return nil, errors.New("no such key")
	}
	return testKey, nil
}
