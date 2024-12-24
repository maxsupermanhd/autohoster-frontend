package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/georgysavva/scany/v2/pgxscan"
	"github.com/gorilla/mux"
	"github.com/jackc/pgx/v5/pgxpool"
)

type RatingCategory struct {
	ID         int
	TimeStarts **time.Time
	TimeEnds   **time.Time
	Name       string
	Variant    string
}

func GetRatingCategories(ctx context.Context, db *pgxpool.Pool) ([]*RatingCategory, error) {
	r := []*RatingCategory{}
	return r, pgxscan.Select(ctx, db, &r, `SELECT * FROM rating_categories`)
}

func GetRatingCategory(ctx context.Context, db *pgxpool.Pool, id int) (*RatingCategory, error) {
	r := []*RatingCategory{}
	err := pgxscan.Select(ctx, db, &r, `SELECT * FROM rating_categories WHERE id = $1`, id)
	if err != nil {
		return nil, err
	}
	if len(r) != 1 {
		return nil, errors.New("rating category id collision, shit is on fire")
	}
	return r[0], nil
}

type LeaderboardEntry struct {
	Name     string `db:"display_name"`
	Account  int
	Category int            `json:"-"`
	Variant  string         `json:"-"`
	Rating   map[string]any `db:"data"`
}

func GetLeaderboardTop(ctx context.Context, db *pgxpool.Pool, category int, limit int) ([]*LeaderboardEntry, error) {
	r := []*LeaderboardEntry{}
	return r, pgxscan.Select(ctx, db, &r, `SELECT * FROM leaderboard2 WHERE category = $1 ORDER BY (data->'elo')::int DESC LIMIT $2`, category, limit)
}

func LeaderboardsHandler(w http.ResponseWriter, r *http.Request) {
	cats, err := GetRatingCategories(r.Context(), dbpool)
	if err != nil {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": err.Error()})
		return
	}
	lb := map[*RatingCategory][]*LeaderboardEntry{}
	for _, c := range cats {
		l, err := GetLeaderboardTop(r.Context(), dbpool, c.ID, 3)
		if err != nil {
			basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": err.Error()})
			return
		}
		for _, ll := range l {
			ll.Rating["t"] = c.Variant
		}
		lb[c] = l
	}
	basicLayoutLookupRespond("leaderboards", w, r, map[string]any{"leaderboards": lb})
}

func LeaderboardHandler(w http.ResponseWriter, r *http.Request) {
	categoryIdString, ok := mux.Vars(r)["category"]
	if !ok {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "no rating category"})
		return
	}
	categoryId, err := strconv.Atoi(categoryIdString)
	if err != nil {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": err.Error()})
		return
	}
	category, err := GetRatingCategory(r.Context(), dbpool, categoryId)
	if err != nil {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": err.Error()})
		return
	}
	basicLayoutLookupRespond("leaderboard", w, r, map[string]any{"category": category})
}

func APIgetLeaderboard(_ http.ResponseWriter, r *http.Request) (int, any) {
	categorys, ok := mux.Vars(r)["category"]
	if !ok {
		return 500, "no category"
	}
	category, err := strconv.Atoi(categorys)
	if err != nil {
		return 500, err
	}
	return genericViewRequest[LeaderboardEntry](r, genericRequestParams{
		tableName:               "leaderboard2",
		limitClamp:              500,
		sortDefaultOrder:        "desc",
		sortDefaultColumn:       "(data->'elo')::int",
		sortColumns:             []string{"display_name", "category", "(data->'elo')::int", "(data->'played')::int", "(data->'won')::int", "(data->'lost')::int", "(data->'time_played')::int"},
		filterColumnsFull:       []string{"category", "(data->'elo')::int", "(data->'played')::int", "(data->'won')::int", "(data->'lost')::int", "(data->'time_played')::int"},
		filterColumnsStartsWith: []string{"display_name"},
		searchColumn:            "display_name",
		searchSimilarity:        0.3,
		addWhereCase:            fmt.Sprintf("category = %d AND (data->'played')::int > 0", category),
		columnMappings: map[string]string{
			"Won":    "(data->'won')::int",
			"Lost":   "(data->'lost')::int",
			"Elo":    "(data->'elo')::int",
			"Played": "(data->'played')::int",
			"Name":   "display_name",
		},
	})
}
