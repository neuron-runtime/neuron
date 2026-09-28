package capabilityruntime

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// IsExactVersion reports whether version is a plain semver pin ("1.2.0")
// rather than a constraint ("^1.0.0") or a floating requirement ("").
func IsExactVersion(version string) bool {
	if strings.TrimSpace(version) == "" {
		return false
	}
	_, err := semver.NewVersion(version)
	return err == nil
}

func SelectVersion(
	constraint string,
	versions []string,
) (string, error) {

	if len(versions) == 0 {
		return "", fmt.Errorf("no capability runtime versions available")
	}

	if constraint == "" {
		// Latest valid semver version.
		var latest *semver.Version
		for _, raw := range versions {
			v, err := semver.NewVersion(raw)
			if err != nil {
				continue
			}
			if latest == nil || v.GreaterThan(latest) {
				latest = v
			}
		}
		if latest == nil {
			return "", fmt.Errorf("no valid semver capability runtime versions available")
		}
		return latest.String(), nil
	}

	c, err := semver.NewConstraint(constraint)

	if err != nil {
		return "", fmt.Errorf(
			"invalid capability runtime version constraint %q: %w",
			constraint,
			err,
		)
	}

	candidates := make([]*semver.Version, 0)

	for _, raw := range versions {
		v, err := semver.NewVersion(raw)
		if err != nil {
			continue
		}

		if c.Check(v) {
			candidates = append(candidates, v)
		}
	}

	if len(candidates) == 0 {
		return "", fmt.Errorf(
			"no capability runtime version satisfies %q",
			constraint,
		)
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].GreaterThan(candidates[j])
	})

	return candidates[0].String(), nil
}
