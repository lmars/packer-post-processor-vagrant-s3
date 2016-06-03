package main

import (
	"fmt"

	"github.com/blang/semver"
)

const NoVersion = "0.0.0"

type Manifest struct {
	Name     string     `json:"name"`
	Versions []*Version `json:"versions"`
}

func (m *Manifest) add(version string, provider *Provider) error {
	for _, w := range m.Versions {
		if w.Version == version {
			for _, p := range w.Providers {
				if p.Name == provider.Name {
					return fmt.Errorf("%s box already exists in manifest for version %s", p.Name, version)
				}
			}
			w.Providers = append(w.Providers, provider)
			return nil
		}
	}
	m.Versions = append(m.Versions, &Version{
		Version:   version,
		Providers: []*Provider{provider},
	})
	return nil
}

func (m *Manifest) getLatestVersion() string {
	latestVersion, _ := semver.Make(NoVersion)

	for _, version := range m.Versions {
		if currentVersion, err := semver.Make(version.Version); err != nil {
			continue
		} else if latestVersion.LT(currentVersion) {
			latestVersion = currentVersion
		}
	}

	return latestVersion.String()
}

func (m *Manifest) getNextVersion() string {
	latestVersion, _ := semver.Make(m.getLatestVersion())
	latestVersion.Minor++
	latestVersion.Patch = 0

	return latestVersion.String()
}

type Version struct {
	Version   string      `json:"version"`
	Providers []*Provider `json:"providers"`
}

type Provider struct {
	Name         string `json:"name"`
	Url          string `json:"url"`
	ChecksumType string `json:"checksum_type"`
	Checksum     string `json:"checksum"`
}
