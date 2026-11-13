package ytmusic

import (
	"context"
	"encoding/json"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type DiscoverResult struct {
	Songs   []TrackItem  `json:"songs"`
	Artists []ArtistItem `json:"artists"`
	Source  string       `json:"source,omitempty"` // spotify | apple | youtube
}

func (y *YTMusicEngine) RandomHome(ctx context.Context) (*DiscoverResult, error) {
	d, err := y.Global20(ctx)
	if err != nil {
		return nil, err
	}
	rand.New(rand.NewSource(time.Now().UnixNano())).Shuffle(len(d.Songs), func(i, j int) { d.Songs[i], d.Songs[j] = d.Songs[j], d.Songs[i] })
	if len(d.Songs) > 12 {
		d.Songs = d.Songs[:12]
	}
	return d, nil
}

type itunesSearchResponse struct {
	Results []struct {
		TrackName      string `json:"trackName"`
		ArtistName     string `json:"artistName"`
		CollectionName string `json:"collectionName"`
		Artwork        string `json:"artworkUrl100"`
	} `json:"results"`
}

// EnrichAppleArtwork memprioritaskan artwork Apple Music pada hasil pencarian.
// Bila Apple sedang tidak tersedia, hasil YouTube Music tetap dipakai tanpa menggagalkan pencarian.
func EnrichAppleArtwork(ctx context.Context, query string, result *SearchResult) {
	if result == nil || strings.TrimSpace(query) == "" {
		return
	}
	u := "https://itunes.apple.com/search?term=" + url.QueryEscape(query) + "&entity=song&limit=40"
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return
	}
	req.Header.Set("User-Agent", "Jemci/3.5")
	cl := &http.Client{Timeout: 7 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return
	}
	var data itunesSearchResponse
	if json.NewDecoder(resp.Body).Decode(&data) != nil {
		return
	}
	art := func(s string) string { return strings.Replace(s, "100x100bb", "600x600bb", 1) }
	normalize := func(s string) string { return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(s)), " ")) }
	for i := range result.Songs {
		t := &result.Songs[i]
		title := normalize(t.Title)
		artist := normalize(t.Artist)
		for _, a := range data.Results {
			if a.Artwork == "" {
				continue
			}
			at := normalize(a.TrackName)
			aa := normalize(a.ArtistName)
			if title == at || (len(title) > 5 && (strings.Contains(at, title) || strings.Contains(title, at))) {
				if artist == "" || aa == "" || strings.Contains(aa, artist) || strings.Contains(artist, aa) {
					t.Thumbnails = []ThumbnailItem{{URL: art(a.Artwork), Width: 600, Height: 600}}
					break
				}
			}
		}
	}
	for i := range result.Albums {
		album := &result.Albums[i]
		name := normalize(album.Title)
		for _, a := range data.Results {
			if a.Artwork != "" && normalize(a.CollectionName) == name {
				album.Thumbnails = []ThumbnailItem{{URL: art(a.Artwork), Width: 600, Height: 600}}
				break
			}
		}
	}
}

func spotifyURLID(q string) (string, bool) {
	u, err := url.Parse(q)
	if err != nil {
		return "", false
	}
	if !strings.Contains(strings.ToLower(u.Host), "spotify.com") {
		return "", false
	}
	p := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i := 0; i+1 < len(p); i++ {
		if p[i] == "playlist" {
			return p[i+1], true
		}
	}
	return "", false
}
