package main

import (
	"context"
	"encoding/json"
	"html/template"
	"log"
	"math"
	"net/http"

	"github.com/davecgh/go-spew/spew"
	"github.com/jackc/pgx/v4"
)

type Elo struct {
	ID         int `json:"ID"`
	Elo        int `json:"elo"`
	Elo2       int `json:"elo2"`
	Autowon    int `json:"autowon"`
	Autolost   int `json:"autolost"`
	Autoplayed int `json:"autoplayed"`
}

type EloGamePlayer struct {
	ID       int
	Team     int
	Usertype string
	EloDiff  int
}

type EloGame struct {
	ID       int
	GameTime int
	Base     int
	Players  []EloGamePlayer
}

func CalcElo(G *EloGame, P map[int]*Elo) {
	Team1ID := []int{}
	Team2ID := []int{}
	for _, p := range G.Players {
		if p.Team == 0 {
			Team1ID = append(Team1ID, p.ID)
		} else if p.Team == 1 {
			Team2ID = append(Team2ID, p.ID)
		}
	}
	if len(Team1ID) != len(Team2ID) {
		log.Printf("Incorrect length: %d", G.ID)
		return
	}
	Team1EloSum := 0
	Team2EloSum := 0
	for _, p := range Team1ID {
		Team1EloSum += P[p].Elo
	}
	for _, p := range Team2ID {
		Team2EloSum += P[p].Elo
	}
	Team1Won := 0
	Team2Won := 0
	if G.Players[0].Usertype == "winner" {
		SecondTeamFoundLost := false
		for i, p := range G.Players {
			if i == 0 {
				continue
			}
			if p.Team != G.Players[0].Team && p.Usertype == "loser" {
				SecondTeamFoundLost = true
				break
			}
		}
		Team1Won = 1
		if !SecondTeamFoundLost {
			log.Printf("Game %d is sus", G.ID)
			return
		}
	} else if G.Players[0].Usertype == "loser" {
		SecondTeamFoundWon := false
		for i, p := range G.Players {
			if i == 0 {
				continue
			}
			if p.Team != G.Players[0].Team && p.Usertype == "winner" {
				SecondTeamFoundWon = true
				break
			}
		}
		Team2Won = 1
		if !SecondTeamFoundWon {
			log.Printf("Game %d is sus", G.ID)
			return
		}
	}

	Team1EloAvg := Team1EloSum / len(Team1ID)
	Team2EloAvg := Team2EloSum / len(Team2ID)
	log.Printf("Processing game %d", G.ID)
	log.Printf("Team won: %v %v", Team2Won, Team1Won)
	log.Printf("Team avg: %v %v", Team1EloAvg, Team2EloAvg)
	K := float64(20)
	Chance1 := 1 / (1 + math.Pow(float64(10), float64(Team1EloAvg-Team2EloAvg)/float64(400)))
	Chance2 := 1 / (1 + math.Pow(float64(10), float64(Team2EloAvg-Team1EloAvg)/float64(400)))
	log.Printf("Chances: %v %v", Chance1, Chance2)
	diff1 := int(math.Round(K * Chance1))
	diff2 := int(math.Round(K * Chance2))
	log.Printf("diff: %v %v", diff1, diff2)
	var Additive int
	if G.Players[0].Usertype == "winner" {
		Additive = diff1
	} else {
		Additive = diff2
	}
	for pi, p := range G.Players {
		if p.Usertype == "winner" {
			P[p.ID].Elo += Additive
			P[p.ID].Autowon++
			G.Players[pi].EloDiff = Additive
		} else {
			P[p.ID].Elo -= Additive //+ game.GameTime/600
			P[p.ID].Autolost++
			G.Players[pi].EloDiff = -Additive //+ game.GameTime/600
		}
		P[p.ID].Autoplayed++
	}
}

func CalcEloForAll(G []*EloGame, P map[int]*Elo) {
	for _, p := range P {
		p.Elo = 1400
		p.Elo2 = 1400
		p.Autowon = 0
		p.Autolost = 0
		p.Autoplayed = 0
	}
	for gamei, _ := range G {
		CalcElo(G[gamei], P)
	}
}

func EloRecalcHandler(w http.ResponseWriter, r *http.Request) {
	rows, derr := dbpool.Query(context.Background(), `
				SELECT
					games.id as gid, gametime,
					players, teams, usertype,
					array_agg(row_to_json(p))::text[] as pnames
				FROM games
				JOIN players as p ON p.id = any(games.players)
				WHERE deleted = false AND hidden = false AND calculated = true AND finished = true
				GROUP BY gid
				ORDER BY timestarted`)
	if derr != nil {
		if derr == pgx.ErrNoRows {
			basicLayoutLookupRespond("plainmsg", w, r, map[string]interface{}{"msg": "No games played"})
		} else {
			basicLayoutLookupRespond("plainmsg", w, r, map[string]interface{}{"msgred": true, "msg": "Database query error: " + derr.Error()})
		}
		return
	}
	defer rows.Close()
	Games := []*EloGame{}
	Players := map[int]*Elo{}
	for rows.Next() {
		var g EloGame
		var players []int
		var teams []int
		var usertype []string
		var playerinfo []string
		err := rows.Scan(&g.ID, &g.GameTime, &players, &teams, &usertype, &playerinfo)
		if err != nil {
			basicLayoutLookupRespond("plainmsg", w, r, map[string]interface{}{"msgred": true, "msg": "Database scan error: " + err.Error()})
			return
		}
		for _, pv := range playerinfo {
			var e Elo
			err := json.Unmarshal([]byte(pv), &e)
			if err != nil {
				basicLayoutLookupRespond("plainmsg", w, r, map[string]interface{}{"msgred": true, "msg": "Json error: " + err.Error()})
				return
			}
			Players[e.ID] = &e
		}
		for pslt, pid := range players {
			if pid == -1 || pid == 370 {
				continue
			}
			var p EloGamePlayer
			p.Usertype = usertype[pslt]
			p.ID = pid
			p.Team = teams[pslt]
			p.EloDiff = 0
			g.Players = append(g.Players, p)
		}
		Games = append(Games, &g)
	}
	basicLayoutLookupRespond("plainmsg", w, r, map[string]interface{}{"nocenter": true, "msg": template.HTML("<pre>" + spew.Sdump(Players) + spew.Sdump(Games) + "</pre>")})
	CalcEloForAll(Games, Players)
	basicLayoutLookupRespond("plainmsg", w, r, map[string]interface{}{"nocenter": true, "msg": template.HTML("<pre>" + spew.Sdump(Players) + spew.Sdump(Games) + "</pre>")})
	for _, p := range Players {
		log.Printf("Updating player %d: elo %d elo2 %d autowon %d autolost %d autoplayed %d", p.ID, p.Elo, p.Elo2, p.Autoplayed, p.Autowon, p.Autolost)
		tag, derr := dbpool.Exec(context.Background(), "UPDATE players SET elo = $1, elo2 = $2, autoplayed = $3, autowon = $4, autolost = $5 WHERE id = $6",
			p.Elo, p.Elo2, p.Autoplayed, p.Autowon, p.Autolost, p.ID)
		if derr != nil {
			basicLayoutLookupRespond("plainmsg", w, r, map[string]interface{}{"msgred": 1, "msg": "Database call error: " + derr.Error()})
			return
		}
		if tag.RowsAffected() != 1 {
			basicLayoutLookupRespond("plainmsg", w, r, map[string]interface{}{"msgred": 1, "msg": "Database insert error, rows affected " + string(tag)})
			return
		}
	}
	for _, g := range Games {
		var elodiffs []int
		for _, p := range g.Players {
			elodiffs = append(elodiffs, p.EloDiff)
		}
		log.Printf("Updating game %d: elodiff %v ", g.ID, elodiffs)
		tag, derr := dbpool.Exec(context.Background(), "UPDATE games SET elodiff = $1 WHERE id = $2",
			elodiffs, g.ID)
		if derr != nil {
			basicLayoutLookupRespond("plainmsg", w, r, map[string]interface{}{"msgred": 1, "msg": "Database call error: " + derr.Error()})
			return
		}
		if tag.RowsAffected() != 1 {
			basicLayoutLookupRespond("plainmsg", w, r, map[string]interface{}{"msgred": 1, "msg": "Database insert error, rows affected " + string(tag)})
			return
		}
	}
}