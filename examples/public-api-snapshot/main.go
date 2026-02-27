package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/jaeyoung0509/seoul"
)

// Snapshot represents an operations dashboard assembled from multiple public APIs.
type Snapshot struct {
	Repo     RepoSummary
	Release  ReleaseSummary
	Weather  WeatherSummary
	FX       FXSummary
	Warnings []string
}

type RepoSummary struct {
	Name       string
	Stars      int
	OpenIssues int
}

type ReleaseSummary struct {
	Tag         string
	PublishedAt string
}

type WeatherSummary struct {
	TempC float64
}

type FXSummary struct {
	USDKRW float64
}

type update struct {
	apply   func(*Snapshot)
	warning string
}

func main() {
	ctx := context.Background()
	snap, err := BuildOpsSnapshot(ctx)
	if err != nil {
		fmt.Println("snapshot failed:", err)
		return
	}

	fmt.Printf("repo=%s stars=%d open_issues=%d\n", snap.Repo.Name, snap.Repo.Stars, snap.Repo.OpenIssues)
	fmt.Printf("latest_release=%s published_at=%s\n", snap.Release.Tag, snap.Release.PublishedAt)
	fmt.Printf("seoul_temp_c=%.1f usdkrw=%.2f\n", snap.Weather.TempC, snap.FX.USDKRW)
	fmt.Printf("warnings=%v\n", snap.Warnings)
}

// BuildOpsSnapshot concurrently aggregates four public APIs.
//
// Data sources:
//   - GitHub repo metadata (critical)
//   - GitHub latest release (critical)
//   - Open-Meteo current temperature (optional)
//   - ER API USD/KRW rate (optional)
func BuildOpsSnapshot(ctx context.Context) (Snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()

	client := &http.Client{Timeout: 2 * time.Second}
	g := seoul.New[update](
		ctx,
		seoul.WithMaxConcurrency(4),
		seoul.WithFailFast(false),
	)

	if err := g.Go(func(ctx context.Context) (update, error) {
		reqCtx, reqCancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		defer reqCancel()

		var body struct {
			FullName        string `json:"full_name"`
			StargazersCount int    `json:"stargazers_count"`
			OpenIssuesCount int    `json:"open_issues_count"`
		}
		if err := fetchJSON(reqCtx, client, "https://api.github.com/repos/cli/cli", &body); err != nil {
			return update{}, fmt.Errorf("repo metadata: %w", err)
		}
		return update{apply: func(s *Snapshot) {
			s.Repo = RepoSummary{
				Name:       body.FullName,
				Stars:      body.StargazersCount,
				OpenIssues: body.OpenIssuesCount,
			}
		}}, nil
	}); err != nil {
		return Snapshot{}, err
	}

	if err := g.Go(func(ctx context.Context) (update, error) {
		reqCtx, reqCancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		defer reqCancel()

		var body struct {
			TagName     string `json:"tag_name"`
			PublishedAt string `json:"published_at"`
		}
		if err := fetchJSON(reqCtx, client, "https://api.github.com/repos/cli/cli/releases/latest", &body); err != nil {
			return update{}, fmt.Errorf("latest release: %w", err)
		}
		return update{apply: func(s *Snapshot) {
			s.Release = ReleaseSummary{Tag: body.TagName, PublishedAt: body.PublishedAt}
		}}, nil
	}); err != nil {
		return Snapshot{}, err
	}

	if err := g.Go(func(ctx context.Context) (update, error) {
		reqCtx, reqCancel := context.WithTimeout(ctx, 1200*time.Millisecond)
		defer reqCancel()

		var body struct {
			Current struct {
				Temperature2m float64 `json:"temperature_2m"`
			} `json:"current"`
		}
		url := "https://api.open-meteo.com/v1/forecast?latitude=37.5665&longitude=126.9780&current=temperature_2m&timezone=Asia%2FSeoul"
		if err := fetchJSON(reqCtx, client, url, &body); err != nil {
			return update{warning: fmt.Sprintf("weather unavailable: %v", err)}, nil
		}
		return update{apply: func(s *Snapshot) {
			s.Weather = WeatherSummary{TempC: body.Current.Temperature2m}
		}}, nil
	}); err != nil {
		return Snapshot{}, err
	}

	if err := g.Go(func(ctx context.Context) (update, error) {
		reqCtx, reqCancel := context.WithTimeout(ctx, 1200*time.Millisecond)
		defer reqCancel()

		var body struct {
			Rates map[string]float64 `json:"rates"`
		}
		if err := fetchJSON(reqCtx, client, "https://open.er-api.com/v6/latest/USD", &body); err != nil {
			return update{warning: fmt.Sprintf("fx unavailable: %v", err)}, nil
		}
		krw, ok := body.Rates["KRW"]
		if !ok {
			return update{warning: "fx unavailable: KRW rate missing"}, nil
		}
		return update{apply: func(s *Snapshot) {
			s.FX = FXSummary{USDKRW: krw}
		}}, nil
	}); err != nil {
		return Snapshot{}, err
	}

	g.Close()

	var snap Snapshot
	for res := range g.Results(ctx) {
		if res.Err != nil {
			continue
		}
		if res.Value.warning != "" {
			snap.Warnings = append(snap.Warnings, res.Value.warning)
		}
		if res.Value.apply != nil {
			res.Value.apply(&snap)
		}
	}
	sort.Strings(snap.Warnings)

	if err := g.Wait(); err != nil {
		return Snapshot{}, fmt.Errorf("critical data source failed: %w", err)
	}
	return snap, nil
}

func fetchJSON[T any](ctx context.Context, client *http.Client, url string, out *T) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "seoul-example/0.2")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("status %s", resp.Status)
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	return nil
}
