// xray_status.go
//
// Маленький CLI, который показывает текущее состояние Xray.
// По умолчанию использует http://127.0.0.1:8080 и токен из XRAY_API_TOKEN.
// Пример сборки/запуска:
//
//	go build -o xray-status xray_status.go
//	./xray-status --base http://127.0.0.1:8080
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

type statsResponse struct {
	Services []serviceEntry `json:"services"`
	Users    []userEntry    `json:"users"`
}

type serviceEntry struct {
	Tag    string `json:"tag"`
	Type   string `json:"type"`
	Status bool   `json:"status"`
}

type userEntry struct {
	Email    string `json:"email"`
	Uplink   int64  `json:"uplink"`
	Downlink int64  `json:"downlink"`
}

func main() {
	defaultBase := getenvDefault("XRAY_LISTENER__API_BASE", "http://127.0.0.1:8080")
	defaultToken := os.Getenv("XRAY_LISTENER_API_TOKEN")

	base := flag.String("base", defaultBase, "Base URL Xray API (пример: http://127.0.0.1:8080)")
	token := flag.String("token", defaultToken, "Bearer токен Xray API (если нужен)")
	reset := flag.Bool("reset", false, "Сбросить счётчики трафика после чтения")
	timeout := flag.Duration("timeout", 5*time.Second, "HTTP timeout")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	stats, err := fetchStats(ctx, *base, *token, *reset)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[xray-status] ошибка запроса к %s: %v\n", *base, err)
		os.Exit(1)
	}

	printSummary(*base, stats)
}

func fetchStats(ctx context.Context, baseURL, token string, reset bool) (*statsResponse, error) {
	body, err := json.Marshal(struct {
		Reset bool `json:"reset"`
	}{Reset: reset})
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}

	endpoint := strings.TrimRight(baseURL, "/") + "/stats"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("unexpected status %s", resp.Status)
	}

	var parsed statsResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &parsed, nil
}

func printSummary(baseURL string, stats *statsResponse) {
	fmt.Println("=== Xray node status ===")
	fmt.Printf("API endpoint : %s\n", baseURL)

	sort.Slice(stats.Services, func(i, j int) bool {
		return stats.Services[i].Tag < stats.Services[j].Tag
	})
	fmt.Printf("Services     : %d\n", len(stats.Services))
	for _, svc := range stats.Services {
		state := "STOPPED"
		if svc.Status {
			state = "RUNNING"
		}
		fmt.Printf("  - %-8s %-10s tag=%q\n", state, svc.Type, svc.Tag)
	}

	sort.Slice(stats.Users, func(i, j int) bool {
		return stats.Users[i].Email < stats.Users[j].Email
	})
	fmt.Printf("\nUsers        : %d\n", len(stats.Users))
	for _, user := range stats.Users {
		fmt.Printf("  - %-30s ↑ %8s   ↓ %8s\n",
			user.Email,
			formatBytes(user.Uplink),
			formatBytes(user.Downlink),
		)
	}

	fmt.Println("\nПодсказка: переопределите XRAY_API_BASE / XRAY_API_TOKEN или используйте --base/--token.")
}

func formatBytes(v int64) string {
	if v < 0 {
		return "0 B"
	}
	const step = 1024.0
	units := []string{"B", "KB", "MB", "GB", "TB", "PB", "EB"}
	value := float64(v)
	for _, unit := range units {
		if value < step || unit == "EB" {
			return fmt.Sprintf("%.1f %s", value, unit)
		}
		value /= step
	}
	return fmt.Sprintf("%.1f EB", value)
}

func getenvDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
