package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"slices"
	"sort"
	"strconv"

	"github.com/davecgh/go-spew/spew"
	"github.com/gorilla/mux"
	"github.com/jackc/pgx/v5"
)

func APIgetResearchlogData(_ http.ResponseWriter, r *http.Request) (int, any) {
	params := mux.Vars(r)
	gid := params["gid"]
	var j []map[string]any
	err := dbpool.QueryRow(context.Background(), `SELECT coalesce(research_log, '[]')::jsonb FROM games WHERE id = $1`, gid).Scan(&j)
	if err != nil {
		if err == pgx.ErrNoRows {
			return http.StatusNoContent, nil
		}
		return 500, err
	}
	for i := range j {
		for k, v := range j[i] {
			if k == "name" {
				j[i][k] = getResearchName(v.(string))
				j[i]["id"] = v.(string)
			}
		}
	}
	return 200, j
}

var (
	researchSummaryPaths [][]string
)

func LoadResearchSummaryPaths() (ret [][]string, err error) {
	var content []byte
	content, err = os.ReadFile(cfg.GetDSString("researchSummaryPaths.json", "researchSummaryPaths"))
	if err != nil {
		return
	}
	err = json.Unmarshal(content, &ret)
	return
}

func APIgetResearchSummary(w http.ResponseWriter, r *http.Request) (int, any) {
	params := mux.Vars(r)
	gid := params["gid"]
	var researchLog []resEntry
	var players []Player
	var settingAlliance int
	err := dbpool.QueryRow(context.Background(), `SELECT
	research_log::jsonb,
	json_agg(json_build_object(
		'Position', p.position,
		'Team', p.team,
		'Usertype', p.usertype,
		'Color', p.color,
		'Identity', i.id,
		'IdentityPubKey', encode(i.pkey, 'hex'),
		'Account', a.id,
		'DisplayName', coalesce(n.display_name, substring(encode(i.pkey, 'hex') for 5)),
		'ClearName', coalesce(n.clear_name, ''),
		'Rating', (select r from rating as r where r.category = g.display_category and r.account = i.account),
		'Props', p.props
	))::jsonb,
	setting_alliance
FROM games as g
JOIN players as p on g.id = p.game
JOIN identities as i on p.identity = i.id
LEFT JOIN accounts as a on a.id = i.account
LEFT JOIN names as n on n.id = a.name
WHERE g.id = $1 and research_log is not null
GROUP BY 1, 3`, gid).Scan(&researchLog, &players, &settingAlliance)
	if err != nil {
		if err == pgx.ErrNoRows {
			return http.StatusNoContent, nil
		}
		return 500, err
	}

	displayTeams := settingAlliance == 2 && len(players) > 2

	teams := []struct {
		index     int
		positions []int
	}{}

	for _, pl := range players {
		tf := -1
		for t := range teams {
			if teams[t].index == pl.Team {
				tf = t
				break
			}
		}
		if tf == -1 {
			teams = append(teams, struct {
				index     int
				positions []int
			}{
				index:     pl.Team,
				positions: []int{pl.Position},
			})
		} else {
			teams[tf].positions = append(teams[tf].positions, pl.Position)
		}
	}

	topTimes := map[string]resEntry{}

	for _, v := range researchLog {
		tt, ok := topTimes[v.Name]
		if ok {
			if tt.Time >= v.Time {
				topTimes[v.Name] = v
			}
		} else {
			topTimes[v.Name] = v
		}
	}

	sort.Slice(players, func(i, j int) bool {
		return players[i].Position < players[j].Position
	})

	findResTime := func(key string, pos int) int {
		for _, v := range researchLog {
			if v.Name == key && int(v.Position) == pos {
				return int(v.Time)
			}
		}
		return -1
	}
	findTeamResTime := func(key string, pos []int) int {
		for _, v := range researchLog {
			if v.Name == key && slices.Contains(pos, int(v.Position)) {
				return int(v.Time)
			}
		}
		return -1
	}

	ret := `<style>
	.rs td {
		border: solid 1px;
		padding: 2px;
	}
	.rs {
		border-collapse: separate;
		border-spacing: 0px;
	}
	</style>
	<script>
	function rsToggle(id) {
		let els = document.querySelectorAll(id);
		console.log(els);
		for (const el of els) {
			if (el.style.display === "none") {
				el.style.display = "table-row";
			} else {
				el.style.display = "none";
			}
		}
	}
	</script>
	`
	ret += `<table class="rs">`
	for i, v := range researchSummaryPaths {
		respathTable := ""
		resShown := 0
		for _, r := range v[1:] {
			if _, ok := topTimes[r]; !ok {
				continue
			}
			resShown++
			respathTable += fmt.Sprintf(`<tr class="rsPath%d" style="display: none;">`, i)
			respathTable += `<td><a href="https://betaguide.wz2100.net/research.html?details_id=` + r + `">
	<img src="https://betaguide.wz2100.net/img/data_icons/Research/` + getResearchName(r) + `.gif"></a></td>`
			respathTable += `<td><a href="https://betaguide.wz2100.net/research.html?details_id=` + r + `">` + getResearchName(r) + `<br>` + r + `</a></td>`

			if displayTeams {
				for t := range teams {
					tRes := findTeamResTime(r, teams[t].positions)
					tcont := `∅`
					tcol := ``
					if tRes != -1 {
						tcont = GameTimeToStringI(tRes)
						tcol = ` style="color: green;" `
						if float64(tRes)-topTimes[r].Time > 16000 {
							tcol = ` style="color: darkorange;" `
						}
						if float64(tRes)-topTimes[r].Time > 31000 {
							tcol = ` style="color: red;" `
						}
					}
					respathTable += fmt.Sprintf(`<td %s >%v</td>`, tcol, tcont)
				}
			} else {
				for _, pl := range players {
					tRes := findResTime(r, pl.Position)
					tcont := `∅`
					tcol := ``
					if tRes != -1 {
						tcont = GameTimeToStringI(tRes)
						tcol = ` style="color: green;" `
						if float64(tRes)-topTimes[r].Time > 16000 {
							tcol = ` style="color: darkorange;" `
						}
						if float64(tRes)-topTimes[r].Time > 31000 {
							tcol = ` style="color: red;" `
						}
					}
					respathTable += fmt.Sprintf(`<td %s >%v</td>`, tcol, tcont)
				}
			}
			respathTable += `</tr>`
		}
		if resShown != 0 {
			ret += `<tr>`
			ret += fmt.Sprintf(`<td><a onclick="rsToggle('.rsPath%d');">👁</a></td>`, i)
			ret += `<td>` + v[0] + `</td>`
			if displayTeams {
				for _, t := range teams {
					ret += fmt.Sprintf(`<td>%c</td>`, "ABCDEFGHIJKLM"[t.index])
				}
			} else {
				for _, pl := range players {
					ret += fmt.Sprintf(`<td class="wz-color-background-%d">%s</td>`, pl.Color, pl.DisplayName)
				}
			}
			ret += `</tr>`
			ret += respathTable
		}
	}
	ret += `</table>`

	w.WriteHeader(200)
	w.Write([]byte(ret))
	return 0, nil
}

type resEntry struct {
	Name     string  `json:"name"`
	Position float64 `json:"position"`
	Time     float64 `json:"time"`
}

var (
	researchClassification []map[string]string
)

func LoadClassification() (ret []map[string]string, err error) {
	var content []byte
	content, err = os.ReadFile(cfg.GetDSString("classification.json", "researchClassification"))
	if err != nil {
		return
	}
	err = json.Unmarshal(content, &ret)
	return
}

// CountClassification in: classification, research out: position[research[time]]
func CountClassification(resl []resEntry) (ret map[int]map[string]int) {
	cl := map[string]string{}
	ret = map[int]map[string]int{}
	for _, b := range researchClassification {
		cl[b["name"]] = b["Subclass"]
	}
	for _, b := range resl {
		if b.Time < 10 {
			continue
		}
		j, f := cl[b.Name]
		if f {
			_, ff := ret[int(b.Position)]
			if !ff {
				ret[int(b.Position)] = map[string]int{}
			}
			_, ff = ret[int(b.Position)][j]
			if ff {
				ret[int(b.Position)][j]++
			} else {
				ret[int(b.Position)][j] = 1
			}
		}
	}
	return
}

func getPlayerClassifications(accountID int) (total, recent map[string]int, err error) {
	total = map[string]int{}
	recent = map[string]int{}
	rows, err := dbpool.Query(context.Background(),
		`select g.research_log, p."position"
from games as g
join players as p on g.id = p.game
join identities as i on i.id = p.identity
join accounts as a on a.id = i.account
where a.id = $1 and g.research_log is not null
order by g.id desc`, accountID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return
		}
		return
	}
	type gameResearch struct {
		playerpos int
		research  string
		cl        map[int]map[string]int
	}
	games := []gameResearch{}
	for rows.Next() {
		g := gameResearch{}
		err = rows.Scan(&g.research, &g.playerpos)
		if err != nil {
			return
		}
		games = append(games, g)
	}
	for i, g := range games {
		var resl []resEntry
		err = json.Unmarshal([]byte(g.research), &resl)
		if err != nil {
			log.Print(err.Error())
			log.Print(spew.Sdump(g))
			continue
		}
		games[i].cl = CountClassification(resl)
		for v, c := range games[i].cl[g.playerpos] {
			if val, ok := total[v]; ok {
				total[v] = val + c
			} else {
				total[v] = c
			}
		}
		if i < 20 {
			for v, c := range games[i].cl[g.playerpos] {
				if val, ok := recent[v]; ok {
					recent[v] = val + c
				} else {
					recent[v] = c
				}
			}
		}
	}
	err = nil
	return
}

func APIresearchClassification(_ http.ResponseWriter, r *http.Request) (int, any) {
	params := mux.Vars(r)
	accounts := params["account"]
	account, err := strconv.Atoi(accounts)
	if err != nil {
		return 400, nil
	}
	a, b, err := getPlayerClassifications(account)
	_ = a
	_ = b
	_ = err
	return 200, a
}
