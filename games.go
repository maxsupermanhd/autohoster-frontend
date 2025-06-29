package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"image/png"
	"log"
	"net/http"
	"regexp"
	"runtime/debug"
	"slices"
	_ "sort"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/jackc/pgx/v5"
)

type Player struct {
	Position       int
	DisplayName    string
	ClearName      string
	Team           int
	Color          int
	Identity       int
	IdentityPubKey string
	Usertype       string
	Rating         map[string]any
	Account        int
	Props          map[string]any
}

type Game struct {
	ID              int
	Version         string
	Instance        int
	TimeStarted     time.Time
	TimeEnded       *time.Time
	GameTime        *int
	SettingScavs    int
	SettingAlliance int
	SettingPower    int
	SettingBase     int
	MapName         string
	MapHash         string
	Mods            string
	Deleted         bool
	Hidden          bool
	Calculated      bool
	DebugTriggered  bool
	Players         []Player
	ReplayFound     bool
	DisplayCategory int
}

func DbGameDetailsHandler(w http.ResponseWriter, r *http.Request) {
	requestedIdentifier := mux.Vars(r)["id"]
	tid := time.Now()
	err := tid.UnmarshalText([]byte(requestedIdentifier))
	if err != nil {
		gid, rerr := strconv.Atoi(requestedIdentifier)
		if rerr == nil {
			var suggestTID time.Time
			derr := dbpool.QueryRow(r.Context(), "select time_started from games where id = $1", gid).Scan(&suggestTID)
			if derr == nil {
				stid, stiderr := suggestTID.MarshalText()
				if stiderr == nil {
					basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msg": template.HTML(`This looks like a number and not like a game start timestamp, however, database has game with such id: <a href="/games/` + string(stid) + `">link</a>`)})
					return
				}
			}
		}
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Invalid id: " + err.Error()})
		return
	}
	req := `with
	rBotAutoDumbWon as (select count(*) from players where identity = 12071 and usertype = 'winner'),
	rBotAutoDumbPlayed as (select count(*) from players where identity = 12071)
select
	g.id, g.version, g.instance, g.time_started, g.time_ended, g.game_time,
	g.setting_scavs, g.setting_alliance, g.setting_power, g.setting_base,
	g.map_name, g.map_hash, g.mods, g.deleted, g.hidden, g.calculated, g.debug_triggered,
	g.display_category,
	jsonb_pretty(json_agg(json_build_object(
		'Position', p.position,
		'Team', p.team,
		'Usertype', p.usertype,
		'Color', p.color,
		'Identity', i.id,
		'IdentityPubKey', encode(i.pkey, 'hex'),
		'Account', a.id,
		'DisplayName', coalesce(n.display_name, ''),
		'ClearName', coalesce(n.clear_name, ''),
		'Rating', CASE  WHEN i.id = 12071 THEN json_build_object(
							't', 'botwl',
							'won', (select * from rBotAutoDumbWon),
							'played', (select * from rBotAutoDumbPlayed))
						ELSE (select to_json(r) from rating as r where r.category = g.display_category and r.account = i.account)
				  END,
		'Props', p.props
	))::jsonb) as players
from games as g
join players as p on p.game = g.id
join identities as i on i.id = p.identity
left join accounts as a on a.id = i.account
left join names as n on n.id = a.name
where g.time_started = $1
group by g.id`
	g := Game{}
	g.Players = []Player{}
	playersJSON := ""
	err = dbpool.QueryRow(r.Context(), req, tid).Scan(&g.ID, &g.Version, &g.Instance, &g.TimeStarted, &g.TimeEnded, &g.GameTime,
		&g.SettingScavs, &g.SettingAlliance, &g.SettingPower, &g.SettingBase,
		&g.MapName, &g.MapHash, &g.Mods, &g.Deleted, &g.Hidden, &g.Calculated, &g.DebugTriggered, &g.DisplayCategory,
		&playersJSON)
	if err != nil {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Something gone wrong, contact administrator."})
		notifyErrorWebhook(fmt.Sprintf("%s\n%s", err.Error(), string(debug.Stack())))
		return
	}
	err = json.Unmarshal([]byte(playersJSON), &g.Players)
	if err != nil {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Something gone wrong, contact administrator."})
		notifyErrorWebhook(fmt.Sprintf("%s\n%s", err.Error(), string(debug.Stack())))
		return
	}
	g.ReplayFound = checkReplayExistsInStorage(r.Context(), g.ID)
	// slices.SortFunc(gmsStage[0].Players, func(a Player, b Player) int {
	// 	return a.Position - b.Position
	// })

	slotColors := [10]int{}
	for _, v := range g.Players {
		slotColors[v.Position] = v.Color
	}
	previewImage, err := getMapPreviewWithColors(g.MapHash, slotColors)
	if err != nil {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Something gone wrong, contact administrator."})
		notifyErrorWebhook(fmt.Sprintf("%s\n%s", err.Error(), string(debug.Stack())))
		return
	}

	previewImageBuf := bytes.NewBufferString("")
	err = png.Encode(previewImageBuf, previewImage)
	if err != nil {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Something gone wrong, contact administrator."})
		notifyErrorWebhook(fmt.Sprintf("%s\n%s", err.Error(), string(debug.Stack())))
		return
	}

	basicLayoutLookupRespond("gamedetails2", w, r, map[string]any{"Game": g, "Preview": base64.RawStdEncoding.EncodeToString(previewImageBuf.Bytes())})
}

func DbGamesHandler(w http.ResponseWriter, r *http.Request) {
	var dMapList []string
	var dTotal int
	var dGamesMinDate time.Time
	var dGamesMaxDate time.Time

	err := RequestMultiple(func() error {
		err := dbpool.QueryRow(r.Context(), `select array_agg(distinct map_name) from games where hidden = false and deleted = false;`).Scan(&dMapList)
		if err != nil && err != pgx.ErrNoRows {
			return err
		}
		return nil
	}, func() error {
		err := dbpool.QueryRow(r.Context(), `select count(*) from games where hidden = false and deleted = false;`).Scan(&dTotal)
		if err != nil && err != pgx.ErrNoRows {
			return err
		}
		return nil
	}, func() error {
		err := dbpool.QueryRow(r.Context(), `select min(time_started), max(time_started) from games where hidden = false and deleted = false;`).Scan(&dGamesMinDate, &dGamesMaxDate)
		if err != nil && err != pgx.ErrNoRows {
			return err
		}
		return nil
	})

	if err != nil {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Something gone wrong, contact administrator."})
		notifyErrorWebhook(fmt.Sprintf("%s\n%s", err.Error(), string(debug.Stack())))
		return
	}

	dMapMap := map[string]string{}
	for _, v := range dMapList {
		dMapMap[v] = v
	}
	basicLayoutLookupRespond("games2", w, r, map[string]any{
		"Total": dTotal,
		"Maps":  dMapMap,
		"GameDateFilterConstraints": map[string]string{
			"min": dGamesMinDate.Format(time.DateOnly),
			"max": dGamesMaxDate.Format(time.DateOnly),
		},
	})
}

func APIgetGames(_ http.ResponseWriter, r *http.Request) (int, any) {
	reqLimit := parseQueryInt(r, "limit", 50)
	if reqLimit > 200 {
		reqLimit = 200
	}
	if reqLimit <= 0 {
		reqLimit = 1
	}
	limiter := fmt.Sprintf("limit %d", reqLimit)
	reqOffset := parseQueryInt(r, "offset", 0)
	if reqOffset < 0 {
		reqOffset = 0
	}
	offset := fmt.Sprintf("offset %d", reqOffset)
	reqSortOrder := parseQueryStringFiltered(r, "order", "desc", "asc")
	reqSortField := parseQueryStringMapped(r, "sort", "time_started", map[string]string{
		"TimeStarted": "time_started",
		"TimeEnded":   "time_ended",
		"ID":          "id",
		"MapName":     "map_name",
		"GameTime":    "game_time",
	})

	reqFilterFields := map[string]string{}
	reqDoFilters := false
	if reqFilterJ := parseQueryString(r, "filter", ""); reqFilterJ != "" {
		err := json.Unmarshal([]byte(reqFilterJ), &reqFilterFields)
		if err == nil && len(reqFilterFields) > 0 {
			reqDoFilters = true
		}
	}

	whereplayerscase := ""
	wherecase := "where deleted = false and hidden = false"
	whereargs := []any{}
	if isSuperadmin(r.Context(), sessionGetUsername(r)) {
		wherecase = ""
	}
	if playerPubKey := parseQueryString(r, "player", ""); playerPubKey != "" {
		whereplayerscase = "where $1 = encode(i.pkey, 'hex')"
		whereargs = append(whereargs, playerPubKey)
	} else if clearName := parseQueryString(r, "clear_name", ""); clearName != "" {
		whereplayerscase = "where $1 = n.clear_name"
		whereargs = append(whereargs, clearName)
	}
	var filterByDate string
	if reqDoFilters {
		val, ok := reqFilterFields["MapName"]
		if ok {
			whereargs = append(whereargs, val)
			if wherecase == "" {
				wherecase = "where g.map_name = $1"
			} else {
				wherecase += fmt.Sprintf(" and g.map_name = $%d", len(whereargs))
			}
		}
		val, ok = reqFilterFields["TimeStarted"]
		if ok {
			filterByDate = val
			whereargs = append(whereargs, val)
			if wherecase == "" {
				wherecase = "where date_trunc('day', g.time_started) = $1"
			} else {
				wherecase += fmt.Sprintf(" and date_trunc('day', g.time_started) = $%d", len(whereargs))
			}
		}
	}

	ordergamescase := fmt.Sprintf("order by %s %s", reqSortField, reqSortOrder)
	ordercase := fmt.Sprintf("order by %s %s", reqSortField, reqSortOrder)
	orderargs := []any{}

	if reqSearch := parseQueryString(r, "search", ""); reqSearch != "" {
		orderargs = []any{reqSearch}
		argnum := len(whereargs) + 1
		ordergamescase = fmt.Sprintf("order by rank () over (order by min(levenshtein(p.clear_name, $%d::text)) desc) desc, %s %s", argnum, reqSortField, reqSortOrder)
		ordercase = fmt.Sprintf("order by rank () over (order by min(levenshtein(n.clear_name, $%d::text)) desc) desc, %s %s", argnum, reqSortField, reqSortOrder)
	}

	var totals int
	var totalsNoFilter int
	var gms []Game
	var filteredMaps []string

	err := RequestMultiple(func() error {
		req := `select count(*) from games where hidden = false and deleted = false;`
		if isSuperadmin(r.Context(), sessionGetUsername(r)) {
			req = `select count(*) from games;`
		}
		return dbpool.QueryRow(r.Context(), req).Scan(&totalsNoFilter)
	}, func() error {
		req := `with wp as (
	select *
	from players as p
	join identities as i on i.id = p.identity
	left join accounts as a on a.id = i.account
	left join names as n on n.id = a.name
	` + whereplayerscase + `
)
select count(distinct g.id)
from games as g
join wp as p on p.game = g.id
` + wherecase
		if cfg.GetDSBool(false, "displayQuery") {
			log.Printf("req 1 %s args %#+v", req, whereargs)
		}
		return dbpool.QueryRow(r.Context(), req, whereargs...).Scan(&totals)
	}, func() error {
		req := `with
	wp as (
		select *
		from players as p
		join identities as i on i.id = p.identity
		left join accounts as a on a.id = i.account
		left join names as n on n.id = a.name
		` + whereplayerscase + `
	),
	wg as (
		select g.id
		from games as g
		join wp as p on p.game = g.id
		` + wherecase + `
		group by g.id
		` + ordergamescase + `
		` + limiter + `
		` + offset + `
	),
	rBotAutoDumbWon as (select count(*) from players where identity = 12071 and usertype = 'winner'),
	rBotAutoDumbPlayed as (select count(*) from players where identity = 12071)
select
	g.id, g.version, g.time_started, g.time_ended, g.game_time,
	g.setting_scavs, g.setting_alliance, g.setting_power, g.setting_base,
	g.map_name, g.map_hash, g.mods, g.deleted, g.hidden, g.calculated, g.debug_triggered,
	g.display_category,
	json_agg(json_build_object(
		'Position', p.position,
		'Team', p.team,
		'Usertype', p.usertype,
		'Color', p.color,
		'Identity', i.id,
		'IdentityPubKey', encode(i.pkey, 'hex'),
		'Account', a.id,
		'DisplayName', coalesce(n.display_name, ''),
		'ClearName', coalesce(n.clear_name, ''),
		'Rating', CASE WHEN i.id = 12071 THEN json_build_object(
							't', 'botwl',
							'won', (select * from rBotAutoDumbWon),
							'played', (select * from rBotAutoDumbPlayed))
						ELSE (select to_json(r) from rating as r where r.category = g.display_category and r.account = i.account)
				 END
	)) as players
from games as g
join wg on wg.id = g.id
join players as p on p.game = g.id
join identities as i on i.id = p.identity
left join accounts as a on a.id = i.account
left join names as n on n.id = a.name
group by g.id
` + ordercase
		args := append(whereargs, orderargs...)
		if cfg.GetDSBool(false, "displayQuery") {
			log.Printf("req 2 %s args %#+v", req, args)
		}
		rows, err := dbpool.Query(r.Context(), req, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			g := Game{}
			playersJSON := ""
			err = rows.Scan(&g.ID, &g.Version, &g.TimeStarted, &g.TimeEnded, &g.GameTime,
				&g.SettingScavs, &g.SettingAlliance, &g.SettingPower, &g.SettingBase,
				&g.MapName, &g.MapHash, &g.Mods, &g.Deleted, &g.Hidden, &g.Calculated, &g.DebugTriggered, &g.DisplayCategory,
				&playersJSON)
			if err != nil {
				return err
			}
			g.Players = []Player{}
			err = json.Unmarshal([]byte(playersJSON), &g.Players)
			if err != nil {
				return err
			}
			slices.SortFunc(g.Players, func(a Player, b Player) int {
				return a.Position - b.Position
			})
			gms = append(gms, g)
		}
		if err != nil {
			return err
		}
		return nil
	}, func() error {
		if filterByDate == "" {
			return nil
		}
		req := `select array_agg(distinct map_name) from games where hidden = false and deleted = false and date_trunc('day', time_started) = $1;`
		if isSuperadmin(r.Context(), sessionGetUsername(r)) {
			req = `select array_agg(distinct map_name) from games where date_trunc('day', time_started) = $1;`
		}
		return dbpool.QueryRow(r.Context(), req, filterByDate).Scan(&filteredMaps)
	})

	if err != nil {
		if errors.Is(err, context.Canceled) {
			return -1, nil
		}
		// basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Something gone wrong, contact administrator."})
		notifyErrorWebhook(fmt.Sprintf("%s\n%s", err.Error(), string(debug.Stack())))
		return 500, err
	}

	if gms == nil {
		gms = []Game{}
	}

	return 200, map[string]any{
		"total":            totals,
		"totalNotFiltered": totalsNoFilter,
		"rows":             gms,
		"filteredMaps":     filteredMaps,
	}
}

func GameTimeToString(t any) string {
	switch v := t.(type) {
	case int:
		return (time.Duration(int(v/1000)) * time.Second).String()
	case *int:
		if v == nil {
			return "nil gametime"
		}
		return (time.Duration(int(*v/1000)) * time.Second).String()
	default:
		return "not float64 gametime"
	}
}
func GameTimeToStringI(t any) string {
	switch v := t.(type) {
	case int:
		return (time.Duration(v/1000) * time.Second).String()
	case *int:
		if v == nil {
			return "nil gametime"
		}
		return (time.Duration(*v/1000) * time.Second).String()
	default:
		return "not int gametime"
	}
}

//lint:ignore U1000 for later
func GameTimeInterToString(t any) string {
	tt, k := t.(float64)
	if k {
		return (time.Duration(int(tt/1000)) * time.Second).String()
	} else {
		return "invalid"
	}
}

//lint:ignore U1000 for later
func SecondsToString(t float64) string {
	return (time.Duration(int(t)) * time.Second).String()
}

//lint:ignore U1000 for later
func SecondsInterToString(t any) string {
	tt, k := t.(float64)
	if k {
		return (time.Duration(int(tt)) * time.Second).String()
	} else {
		return "invalid"
	}
}

var GameDirRegex = regexp.MustCompile(`\./tmp/wz-(\d+)/`)

func GameDirToWeek(p string) int {
	matches := GameDirRegex.FindStringSubmatch(p)
	if len(matches) != 2 {
		log.Println("No match for game directory")
		return -1
	}
	num, err := strconv.Atoi(matches[1])
	if err != nil {
		log.Printf("Error atoi: %#+v %#+v", matches, err)
		return -1
	}
	return num / (7 * 24 * 60 * 60)
}

func InstanceIDToWeek(num int) int {
	return num / (7 * 24 * 60 * 60)
}
