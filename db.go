package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/georgysavva/scany/v2/pgxscan"
)

type genericRequestParams struct {
	tableName               string
	limitClamp              int
	columnMappings          map[string]string
	sortDefaultOrder        string
	sortDefaultColumn       string
	sortColumns             []string
	filterColumnsFull       []string
	filterColumnsStartsWith []string
	filterColumnsExpression map[string]string
	searchColumn            string
	searchSimilarity        float64
	addWhereCase            string
	columnsSpecifier        string
}

func genericViewRequest[T any](r *http.Request, params genericRequestParams) (int, any) {
	reqLimit := max(1, parseQueryInt(r, "limit", 50))
	if reqLimit > params.limitClamp {
		reqLimit = params.limitClamp
	}
	reqOffset := max(0, parseQueryInt(r, "offset", 0))
	reqSortOrder := parseQueryStringFiltered(r, "order", "desc", "asc")
	if params.sortDefaultOrder != "asc" {
		reqSortOrder = parseQueryStringFiltered(r, "order", "asc", "desc")
	}
	reqSortField := parseQueryStringFiltered(r, "sort", params.sortDefaultColumn, params.sortColumns...)
	if mapped, ok := params.columnMappings[reqSortField]; ok {
		reqSortField = mapped
	}

	wherecase := ""
	whereargs := []any{}

	reqFilterJ := parseQueryString(r, "filter", "")
	reqFilterFieldsUnmapped := map[string]string{}
	reqDoFilters := false
	if reqFilterJ != "" {
		err := json.Unmarshal([]byte(reqFilterJ), &reqFilterFieldsUnmapped)
		if err == nil && len(reqFilterFieldsUnmapped) > 0 {
			reqDoFilters = true
		}
	}

	reqFilterFields := map[string]string{}
	for k, v := range reqFilterFieldsUnmapped {
		m, ok := params.columnMappings[k]
		if ok {
			reqFilterFields[m] = v
		}
	}

	if reqDoFilters {
		for _, v := range params.filterColumnsFull {
			val, ok := reqFilterFields[v]
			if ok {
				whereargs = append(whereargs, val)
				if wherecase == "" {
					wherecase = "WHERE " + v + " = $1"
				} else {
					wherecase += " AND " + v + " = $1"
				}
			}
		}
		for _, v := range params.filterColumnsStartsWith {
			val, ok := reqFilterFields[v]
			if ok {
				whereargs = append(whereargs, val)
				if wherecase == "" {
					wherecase = "WHERE starts_with(" + v + ", $1)"
				} else {
					wherecase += fmt.Sprintf(" AND starts_with("+v+", $%d)", len(whereargs))
				}
			}
		}
		for k, v := range params.filterColumnsExpression {
			val, ok := reqFilterFields[k]
			if ok {
				whereargs = append(whereargs, val)
				if wherecase == "" {
					wherecase = fmt.Sprintf("WHERE "+v, 1)
				} else {
					wherecase += fmt.Sprintf(" AND starts_with("+v+", $%d)", len(whereargs))
				}
			}
		}
	}

	if params.addWhereCase != "" {
		if wherecase == "" {
			wherecase = "WHERE " + params.addWhereCase
		} else {
			wherecase += " AND " + params.addWhereCase
		}
	}

	reqSearch := parseQueryString(r, "search", "")
	orderargs := []any{}
	ordercase := fmt.Sprintf("ORDER BY %s %s", reqSortField, reqSortOrder)
	if reqSearch != "" {
		orderargs = []any{reqSearch}
		ordercase = fmt.Sprintf("ORDER BY rank () over (order by similarity(%s, $%d::text) desc), %s %s", params.searchColumn, len(whereargs)+1, reqSortField, reqSortOrder)
	}
	limiter := fmt.Sprintf("LIMIT %d", reqLimit)
	offset := fmt.Sprintf("OFFSET %d", reqOffset)

	columnsSpecifier := "*"
	if params.columnsSpecifier != "" {
		columnsSpecifier = params.columnsSpecifier
	}

	tn := params.tableName

	var totalsNoFilter int
	var totals int
	var rows []*T
	err := RequestMultiple(func() error {
		return dbpool.QueryRow(r.Context(), `SELECT count(`+tn+`) FROM `+tn).Scan(&totalsNoFilter)
	}, func() error {
		return dbpool.QueryRow(r.Context(), `SELECT count(`+tn+`) FROM `+tn+` `+wherecase, whereargs...).Scan(&totals)
	}, func() error {
		req := `SELECT ` + columnsSpecifier + ` FROM ` + tn + ` ` + wherecase + ` ` + ordercase + ` ` + offset + ` ` + limiter
		args := append(whereargs, orderargs...)
		if cfg.GetDSBool(false, "displayQuery") {
			log.Printf("req %q args %#+v", req, args)
		}
		return pgxscan.Select(r.Context(), dbpool, &rows, req, args...)
	})
	if err != nil {
		return 500, err
	}
	return 200, map[string]any{
		"total":            totals,
		"totalNotFiltered": totalsNoFilter,
		"rows":             rows,
	}
}
