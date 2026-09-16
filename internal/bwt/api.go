package bwt

import (
	"context"
	"net/url"
	"strconv"
)

// MaxSubmitBatch is the API's cap on one SubmitUrlBatch call.
const MaxSubmitBatch = 500

func siteParams(site string) url.Values { return url.Values{"siteUrl": {site}} }

// ---- sites ----

// Sites lists the properties the API key's owner has registered.
func (c *Client) Sites(ctx context.Context) ([]Site, error) {
	var out []Site
	err := c.get(ctx, "GetUserSites", nil, &out)
	return out, err
}

// AddSite registers a property. Verification is a separate step.
func (c *Client) AddSite(ctx context.Context, site string) error {
	return c.post(ctx, "AddSite", map[string]any{"siteUrl": site}, nil)
}

// VerifySite asks Bing to re-check the ownership proof (meta tag, XML file or
// DNS record) for a property that has already been added.
func (c *Client) VerifySite(ctx context.Context, site string) (bool, error) {
	var ok bool
	err := c.post(ctx, "VerifySite", map[string]any{"siteUrl": site}, &ok)
	return ok, err
}

// RemoveSite deletes a property from the account.
func (c *Client) RemoveSite(ctx context.Context, site string) error {
	return c.post(ctx, "RemoveSite", map[string]any{"siteUrl": site}, nil)
}

// ---- traffic ----

// QueryStats returns per-query traffic for the whole property.
//
// Bing takes no date range: every one of these calls returns the complete
// history it holds, in its own buckets. Narrowing to a period is the caller's
// job -- see Rows.InRange.
func (c *Client) QueryStats(ctx context.Context, site string) ([]Stats, error) {
	var out []Stats
	err := c.get(ctx, "GetQueryStats", siteParams(site), &out)
	return out, err
}

// PageStats returns per-page traffic. The rows' Query field holds the page URL.
func (c *Client) PageStats(ctx context.Context, site string) ([]Stats, error) {
	var out []Stats
	err := c.get(ctx, "GetPageStats", siteParams(site), &out)
	return out, err
}

// PageQueryStats returns the queries that led to one page.
func (c *Client) PageQueryStats(ctx context.Context, site, page string) ([]Stats, error) {
	var out []Stats
	err := c.get(ctx, "GetPageQueryStats", url.Values{"siteUrl": {site}, "page": {page}}, &out)
	return out, err
}

// QueryPageStats returns the pages that one query led to. The rows' Query field
// holds the page URL.
func (c *Client) QueryPageStats(ctx context.Context, site, query string) ([]Stats, error) {
	var out []Stats
	err := c.get(ctx, "GetQueryPageStats", url.Values{"siteUrl": {site}, "query": {query}}, &out)
	return out, err
}

// RankAndTrafficStats returns site-wide clicks and impressions per bucket. No
// position is reported at this level.
func (c *Client) RankAndTrafficStats(ctx context.Context, site string) ([]TrafficStats, error) {
	var out []TrafficStats
	err := c.get(ctx, "GetRankAndTrafficStats", siteParams(site), &out)
	return out, err
}

// ---- URLs ----

// URLInfo returns what Bing has indexed about one URL.
func (c *Client) URLInfo(ctx context.Context, site, u string) (*URLInfo, error) {
	var out URLInfo
	if err := c.get(ctx, "GetUrlInfo", url.Values{"siteUrl": {site}, "url": {u}}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// URLTrafficInfo returns clicks and impressions for one URL.
func (c *Client) URLTrafficInfo(ctx context.Context, site, u string) (*URLTrafficInfo, error) {
	var out URLTrafficInfo
	if err := c.get(ctx, "GetUrlTrafficInfo", url.Values{"siteUrl": {site}, "url": {u}}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ChildrenURLInfo lists the URLs Bing indexed under a directory. page is
// zero-based. Despite being a read, the API only exposes it over POST.
func (c *Client) ChildrenURLInfo(ctx context.Context, site, dir string, page int) ([]URLInfo, error) {
	var out []URLInfo
	body := map[string]any{
		"siteUrl": site,
		"url":     dir,
		"page":    page,
		// The filter object is required even when nothing is being filtered;
		// 0 is "Any" in every one of its four enums.
		"filterProperties": map[string]any{
			"__type":               "FilterProperties:#Microsoft.Bing.Webmaster.Api",
			"CrawlDateFilter":      0,
			"DiscoveredDateFilter": 0,
			"DocFlagsFilters":      0,
			"HttpCodeFilters":      0,
		},
	}
	err := c.post(ctx, "GetChildrenUrlInfo", body, &out)
	return out, err
}

// ChildrenURLTrafficInfo lists traffic for the URLs under a directory.
func (c *Client) ChildrenURLTrafficInfo(ctx context.Context, site, dir string, page int) ([]URLTrafficInfo, error) {
	var out []URLTrafficInfo
	params := url.Values{"siteUrl": {site}, "url": {dir}, "page": {strconv.Itoa(page)}}
	err := c.get(ctx, "GetChildrenUrlTrafficInfo", params, &out)
	return out, err
}

// ---- submission ----

// SubmitURL submits one URL for (re)crawling.
func (c *Client) SubmitURL(ctx context.Context, site, u string) error {
	return c.post(ctx, "SubmitUrl", map[string]any{"siteUrl": site, "url": u}, nil)
}

// SubmitURLBatch submits up to MaxSubmitBatch URLs in one call.
func (c *Client) SubmitURLBatch(ctx context.Context, site string, urls []string) error {
	return c.post(ctx, "SubmitUrlBatch", map[string]any{"siteUrl": site, "urlList": urls}, nil)
}

// URLSubmissionQuota reports how many URLs may still be submitted.
func (c *Client) URLSubmissionQuota(ctx context.Context, site string) (*Quota, error) {
	var out Quota
	if err := c.get(ctx, "GetUrlSubmissionQuota", siteParams(site), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ContentSubmissionQuota reports the separate allowance for content submission.
func (c *Client) ContentSubmissionQuota(ctx context.Context, site string) (*Quota, error) {
	var out Quota
	if err := c.get(ctx, "GetContentSubmissionQuota", siteParams(site), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---- sitemaps (feeds) ----

// Feeds lists the top-level sitemaps submitted for a property.
func (c *Client) Feeds(ctx context.Context, site string) ([]Feed, error) {
	var out []Feed
	err := c.get(ctx, "GetFeeds", siteParams(site), &out)
	return out, err
}

// FeedDetails lists the child sitemaps of a sitemap index.
func (c *Client) FeedDetails(ctx context.Context, site, feed string) ([]Feed, error) {
	var out []Feed
	err := c.get(ctx, "GetFeedDetails", url.Values{"siteUrl": {site}, "feedUrl": {feed}}, &out)
	return out, err
}

// SubmitFeed submits a sitemap. Bing fetches it asynchronously, so success only
// means the URL was accepted.
func (c *Client) SubmitFeed(ctx context.Context, site, feed string) error {
	return c.post(ctx, "SubmitFeed", map[string]any{"siteUrl": site, "feedUrl": feed}, nil)
}

// RemoveFeed unregisters a sitemap.
func (c *Client) RemoveFeed(ctx context.Context, site, feed string) error {
	return c.post(ctx, "RemoveFeed", map[string]any{"siteUrl": site, "feedUrl": feed}, nil)
}

// ---- crawl ----

// CrawlStats returns bingbot's activity per day.
func (c *Client) CrawlStats(ctx context.Context, site string) ([]CrawlStats, error) {
	var out []CrawlStats
	err := c.get(ctx, "GetCrawlStats", siteParams(site), &out)
	return out, err
}

// CrawlIssues lists URLs bingbot could not fetch cleanly.
func (c *Client) CrawlIssues(ctx context.Context, site string) ([]CrawlIssue, error) {
	var out []CrawlIssue
	err := c.get(ctx, "GetCrawlIssues", siteParams(site), &out)
	return out, err
}

// ---- keywords ----

// Keyword returns impressions for one keyword over a period. Bing answers with
// an empty object when it has no data for the term.
func (c *Client) Keyword(ctx context.Context, q, country, language string, r DateRange) (*Keyword, error) {
	var out Keyword
	params := url.Values{
		"q":         {q},
		"country":   {country},
		"language":  {language},
		"startDate": {r.StartString()},
		"endDate":   {r.EndString()},
	}
	if err := c.get(ctx, "GetKeyword", params, &out); err != nil {
		return nil, err
	}
	if out.Query == "" {
		return nil, nil
	}
	return &out, nil
}

// KeywordStats returns the impression history of one keyword.
func (c *Client) KeywordStats(ctx context.Context, q, country, language string) ([]KeywordStats, error) {
	var out []KeywordStats
	params := url.Values{"q": {q}, "country": {country}, "language": {language}}
	err := c.get(ctx, "GetKeywordStats", params, &out)
	return out, err
}

// RelatedKeywords returns terms Bing considers related to q, with impressions.
func (c *Client) RelatedKeywords(ctx context.Context, q, country, language string, r DateRange) ([]Keyword, error) {
	var out []Keyword
	params := url.Values{
		"q":         {q},
		"country":   {country},
		"language":  {language},
		"startDate": {r.StartString()},
		"endDate":   {r.EndString()},
	}
	err := c.get(ctx, "GetRelatedKeywords", params, &out)
	return out, err
}

// ---- links ----

// LinkCounts returns the site's pages that have inbound links, one page of
// results at a time (zero-based).
func (c *Client) LinkCounts(ctx context.Context, site string, page int) (*LinkCounts, error) {
	var out LinkCounts
	params := url.Values{"siteUrl": {site}, "page": {strconv.Itoa(page)}}
	if err := c.get(ctx, "GetLinkCounts", params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// URLLinks returns the inbound links pointing at one page of the site.
func (c *Client) URLLinks(ctx context.Context, site, link string, page int) (*LinkDetails, error) {
	var out LinkDetails
	params := url.Values{"siteUrl": {site}, "link": {link}, "page": {strconv.Itoa(page)}}
	if err := c.get(ctx, "GetUrlLinks", params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
