package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"arx/arx_go/models"
)

// partNumberingKey is the app_config key holding the base-number config as JSON.
const partNumberingKey = "part_numbering"

// loadBaseNumberConfig returns the configured base-number settings, falling
// back to the built-in default when nothing is saved or the saved JSON is
// unreadable.
func (h *Handler) loadBaseNumberConfig(ctx context.Context) models.BaseNumberConfig {
	raw := h.appConfigGetOr(ctx, partNumberingKey, "")
	if raw == "" {
		return models.DefaultBaseNumberConfig()
	}
	var cfg models.BaseNumberConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil || cfg.Separator == "" || cfg.Width <= 0 {
		return models.DefaultBaseNumberConfig()
	}
	return cfg
}

// nextBaseNumber suggests the next available base-number segment, given the
// configured separator/segment position, by scanning all existing
// part_number values. It is a suggestion only — the DB unique constraint on
// part_number still catches collisions at save time.
func (h *Handler) nextBaseNumber(ctx context.Context) (string, error) {
	cfg := h.loadBaseNumberConfig(ctx)

	rows, err := h.queryContext(ctx, fmt.Sprintf(`SELECT part_number FROM %s`, h.cfg().PartsTable()))
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var partNumbers []string
	for rows.Next() {
		var pn string
		if err := rows.Scan(&pn); err != nil {
			return "", err
		}
		partNumbers = append(partNumbers, pn)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}

	return suggestBaseNumber(cfg, partNumbers), nil
}

// suggestBaseNumber is the pure computation behind nextBaseNumber: parse the
// configured segment out of each part number, then apply cfg.Mode to pick
// the suggestion. Split out so it's testable without a DB.
func suggestBaseNumber(cfg models.BaseNumberConfig, partNumbers []string) string {
	gapFill := cfg.Mode == "next_open_after"
	var used map[int]bool
	if gapFill {
		used = map[int]bool{}
	}
	max := 0
	for _, pn := range partNumbers {
		segs := strings.Split(pn, cfg.Separator)
		if cfg.SegmentIndex < 0 || cfg.SegmentIndex >= len(segs) {
			continue
		}
		n, err := strconv.Atoi(segs[cfg.SegmentIndex])
		if err != nil {
			continue
		}
		if gapFill {
			used[n] = true
		} else if n > max {
			max = n
		}
	}

	var next int
	if gapFill {
		next = cfg.Floor
		for used[next] {
			next++
		}
	} else {
		next = max + 1
	}

	return fmt.Sprintf("%0*d", cfg.Width, next)
}

// PartsNextNumber — GET /api/parts/next-number. Returns the suggested next
// base number as JSON for the New Part form's helper text.
func (h *Handler) PartsNextNumber(w http.ResponseWriter, r *http.Request) {
	suggestion, err := h.nextBaseNumber(r.Context())
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(map[string]string{"suggestion": suggestion})
}

// SettingsPartNumberingSave persists the base-number config to app_config.
func (h *Handler) SettingsPartNumberingSave(w http.ResponseWriter, r *http.Request) {
	if h.database() == nil {
		http.Redirect(w, r, "/settings", http.StatusFound)
		return
	}
	segmentIndex, _ := strconv.Atoi(r.FormValue("segment_index"))
	width, _ := strconv.Atoi(r.FormValue("width"))
	floor, _ := strconv.Atoi(r.FormValue("floor"))
	mode := r.FormValue("mode")
	if mode != "next_open_after" {
		mode = "max_plus_one"
	}
	cfg := models.BaseNumberConfig{
		Separator:    r.FormValue("separator"),
		SegmentIndex: segmentIndex,
		Width:        width,
		Mode:         mode,
		Floor:        floor,
	}
	data, _ := json.Marshal(cfg)
	if err := h.appConfigSet(r.Context(), partNumberingKey, string(data)); err != nil {
		h.renderError(w, r, "Could not save part numbering settings: "+err.Error())
		return
	}
	http.Redirect(w, r, "/settings", http.StatusFound)
}
