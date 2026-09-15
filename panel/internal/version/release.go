package version

import (
	"strconv"
	"strings"
)

func parse(value string) ([3]uint64, bool) {
	var release [3]uint64
	if !strings.HasPrefix(value, "v") {
		return release, false
	}
	parts := strings.Split(value[1:], ".")
	if len(parts) != len(release) {
		return release, false
	}
	for index, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return release, false
		}
		for _, character := range part {
			if character < '0' || character > '9' {
				return release, false
			}
		}
		number, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return release, false
		}
		release[index] = number
	}
	return release, true
}

func IsFormal(value string) bool {
	_, ok := parse(value)
	return ok
}

// Compare returns -1, 0, or 1 for two formal vMAJOR.MINOR.PATCH releases.
func Compare(left, right string) (int, bool) {
	leftRelease, leftOK := parse(left)
	rightRelease, rightOK := parse(right)
	if !leftOK || !rightOK {
		return 0, false
	}
	for index := range leftRelease {
		if leftRelease[index] < rightRelease[index] {
			return -1, true
		}
		if leftRelease[index] > rightRelease[index] {
			return 1, true
		}
	}
	return 0, true
}
