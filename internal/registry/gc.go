package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ListDerivedTags returns full image refs (e.g. "registry:6000/daytona/foo:d-abc...")
// for every ":d-<hex>" tag currently present in the internal registry.
//
// Daytona-internal repos ("daytona-*", "backup-*") are skipped — those hold
// sandbox runtime images and archive backups, not snapshot bases. registryURL
// is the HTTP base URL (e.g. "http://registry:6000"); the host portion of that
// URL is reused as the ref host so refs match what TagByDigest produced.
//
// Used by the orphan-derived-tag GC to find candidates for deletion.
func ListDerivedTags(ctx context.Context, registryURL string) ([]string, error) {
	u, err := url.Parse(registryURL)
	if err != nil {
		return nil, fmt.Errorf("parse registry URL %q: %w", registryURL, err)
	}
	refHost := u.Host
	base := strings.TrimRight(registryURL, "/")
	client := &http.Client{Timeout: 30 * time.Second}

	catReq, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v2/_catalog", nil)
	if err != nil {
		return nil, err
	}
	catResp, err := client.Do(catReq)
	if err != nil {
		return nil, fmt.Errorf("fetch catalog: %w", err)
	}
	defer catResp.Body.Close()
	var catalog struct {
		Repositories []string `json:"repositories"`
	}
	if err := json.NewDecoder(catResp.Body).Decode(&catalog); err != nil {
		return nil, fmt.Errorf("decode catalog: %w", err)
	}

	var refs []string
	for _, repo := range catalog.Repositories {
		leaf := repo
		if idx := strings.LastIndex(repo, "/"); idx >= 0 {
			leaf = repo[idx+1:]
		}
		if strings.HasPrefix(leaf, "daytona-") || strings.HasPrefix(leaf, "backup-") {
			continue
		}

		tagReq, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v2/"+repo+"/tags/list", nil)
		if err != nil {
			return nil, fmt.Errorf("build tags request for repository %s: %w", repo, err)
		}
		tagResp, err := client.Do(tagReq)
		if err != nil {
			return nil, fmt.Errorf("fetch tags for repository %s: %w", repo, err)
		}
		var tagList struct {
			Tags []string `json:"tags"`
		}
		// A corrupted/truncated tag list must NOT be treated as "no tags" — that
		// would silently skip every orphan in this repo while GC reports success.
		decErr := json.NewDecoder(tagResp.Body).Decode(&tagList)
		if cerr := tagResp.Body.Close(); cerr != nil && decErr == nil {
			decErr = cerr
		}
		if decErr != nil {
			return nil, fmt.Errorf("decode tags for repository %s: %w", repo, decErr)
		}
		for _, tag := range tagList.Tags {
			if !strings.HasPrefix(tag, "d-") {
				continue
			}
			refs = append(refs, refHost+"/"+repo+":"+tag)
		}
	}
	return refs, nil
}
