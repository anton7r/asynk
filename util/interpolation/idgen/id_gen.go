package idgen

import (
	"regexp"
	"strings"

	"github.com/anton7r/asynk/util/random"
)

var genIdPattern = regexp.MustCompile(`@{([^}]+)}`)

type GenIDInterpolator struct {
	buildID string
}

// Create build id
func NewGenIDInterpolator() *GenIDInterpolator {
	return &GenIDInterpolator{}
}

func (g GenIDInterpolator) id() (string, error) {
	if g.buildID != "" {
		return g.buildID, nil
	}

	bytes, err := random.RandomBase64String(128 / 8)
	if err != nil {
		return "", err
	}

	return bytes, nil
}

func (g GenIDInterpolator) Interpolate(input string) (string, error) {
	var err error

	return genIdPattern.ReplaceAllStringFunc(input, func(match string) string {
		key := strings.Trim(match[2:len(match)-1], " ")
		var genID string
		genID, err = g.id()

		if key == "GEN_ID" {
			return genID
		}
		return match // Return the original pattern if the env variable is not found
	}), err
}
