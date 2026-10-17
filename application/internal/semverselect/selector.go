// Package semverselect resolves a version requirement against a set of
// available versions. It is domain-neutral on purpose: capability runtime
// resolution and provider resolution share one implementation, and callers
// add their own domain context when wrapping returned errors.
package semverselect

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

// ValidateConstraint reports whether constraint is a valid semver constraint
// ("^1.0.0", "~1.2", ">=1.0.0 <2.0.0"). An empty constraint is valid (it means
// "any"). It is used to reject malformed user input before resolution, so a
// typo is reported as an invalid reference rather than as a missing version.
func ValidateConstraint(constraint string) error {
	if constraint == "" {
		return nil
	}
	_, err := semver.NewConstraint(constraint)
	return err
}

// SelectVersion returns the highest version in versions that satisfies
// constraint. An empty constraint selects the highest valid semver version.
// Versions that do not parse as semver are ignored.
func SelectVersion(constraint string, versions []string) (string, error) {
	if len(versions) == 0 {
		return "", fmt.Errorf("no versions available")
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
			return "", fmt.Errorf("no valid semver versions available")
		}
		return latest.String(), nil
	}

	c, err := semver.NewConstraint(constraint)
	if err != nil {
		return "", fmt.Errorf("invalid version constraint %q: %w", constraint, err)
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
		return "", fmt.Errorf("no version satisfies %q", constraint)
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].GreaterThan(candidates[j])
	})

	return candidates[0].String(), nil
}
