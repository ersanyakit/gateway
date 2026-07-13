package asset

import (
	"core/constants"
	"strings"
)

// CoinLogoURLFromSlug returns the /static/coins URL for a configured coin logo slug.
func CoinLogoURLFromSlug(logoSlug string) string {
	return staticSVGURL("/static/coins", logoSlug)
}

// ChainLogoURL returns the /static/chains URL for a given chain ID.
// Returns empty string for chains without an icon.
func ChainLogoURL(chainID constants.ChainID) string {
	return staticSVGURL("/static/chains", constants.ChainLogoSlug(chainID))
}

func staticSVGURL(prefix, slug string) string {
	slug = strings.ToLower(strings.TrimSpace(slug))
	if slug == "" {
		return ""
	}
	return prefix + "/" + slug + ".svg"
}
