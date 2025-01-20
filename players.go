package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/jackc/pgx/v5"
)

func PlayersHandler(w http.ResponseWriter, r *http.Request) {
	urlID := mux.Vars(r)["id"]
	var accountID int
	err := dbpool.QueryRow(r.Context(), `select account from names where clear_name = $1 and status = 'approved';`, urlID).Scan(&accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		identSpecifier, err := hex.DecodeString(urlID)
		if err != nil {
			basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msg": "Incorrectly formatted identity key or hash (please ensure it has even number of characters when specifying beginning of sha256 hash of public key)"})
			return
		}
		PlayersIdentityHandler(w, r, identSpecifier)
		return
	}
	if DBErr(w, r, err) {
		return
	}
	PlayersAccountHandler(w, r, accountID, urlID)
}

func PlayersIdentityHandler(w http.ResponseWriter, r *http.Request, identSpecifier []byte) {
	var identID int
	var identPubKey *string
	var identHash string
	err := dbpool.QueryRow(r.Context(), `select i.id, encode(i.pkey, 'hex'), i.hash from identities as i where i.pkey = $1 or i.hash ^@ encode($1, 'hex')`, identSpecifier).Scan(&identID, &identPubKey, &identHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msg": "Player not found, identity in url can be hex encoded public key or it's sha256 hash"})
			return
		}
		if !errors.Is(err, context.Canceled) {
			log.Println(err)
			basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msg": "request error"})
			return
		}
	}
	basicLayoutLookupRespond("player", w, r, map[string]any{
		"Player": map[string]any{
			"ID":             identID,
			"IdentityPubKey": identPubKey,
			"IdentityHash":   identHash,
		},
	})
}

func PlayersAccountHandler(w http.ResponseWriter, r *http.Request, accountID int, requestedClearName string) {
	and, err := accGetNamesData(r.Context(), accountID)
	if DBErr(w, r, err) {
		return
	}
	var primaryDisplayName, primaryClearName string
	for _, v := range and.Names {
		if !v.Selected {
			continue
		}
		primaryClearName = v.ClearName
		primaryDisplayName = v.DisplayName
		break
	}

	rows, err := dbpool.Query(r.Context(), `select pkey, hash from identities where account = $1 and pkey is not null`, accountID)
	if DBErr(w, r, err) {
		return
	}
	claimedIdentities := map[string]string{}
	var claimedIdentitiesPkey []byte
	var claimedIdentitiesHash string
	_, err = pgx.ForEachRow(rows, []any{&claimedIdentitiesPkey, &claimedIdentitiesHash}, func() error {
		claimedIdentities[hex.EncodeToString(claimedIdentitiesPkey)] = claimedIdentitiesHash
		return nil
	})
	if DBErr(w, r, err) {
		return
	}

	ChartGamesByPlayercount := newSC("Games by player count", "Game count", "Player count")
	ChartGamesByBaselevel := newSC("Games by base level", "Game count", "Base level")
	ChartGamesByAlliances := newSC("Games by alliance type (2x2+)", "Game count", "Alliance type")
	ChartGamesByScav := newSC("Games by scavengers", "Game count", "Scavengers")
	ResearchClassificationTotal := map[string]int{}
	ResearchClassificationRecent := map[string]int{}

	err = RequestMultiple(func() error {
		var err error
		ResearchClassificationTotal, ResearchClassificationRecent, err = getPlayerClassifications(accountID)
		return err
	}, func() error {
		rows, err := dbpool.Query(r.Context(), `with
	gg as (select p.usertype as usertype, g.id as gid, count(pc) as measure
		from games as g
		join players as pc on g.id = pc.game
		join players as p on g.id = p.game
		join identities as i on i.id = p.identity
		join accounts as a on a.id = i.account
		where a.id = $1
		group by g.id, p.usertype
		order by g.id desc)
select usertype, measure, count(gid)
from gg
where usertype = any('{loser, winner, contender, fighter}')
group by measure, usertype
order by measure, usertype`, accountID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var measure, gameCount int
		var userType string
		_, err = pgx.ForEachRow(rows, []any{&userType, &measure, &gameCount}, func() error {
			switch userType {
			case "loser":
				ChartGamesByPlayercount.appendToColumn(fmt.Sprintf("%dp", measure), "Lost", chartSCcolorLost, gameCount)
			case "winner":
				ChartGamesByPlayercount.appendToColumn(fmt.Sprintf("%dp", measure), "Won", chartSCcolorWon, gameCount)
			}
			return nil
		})
		return err
	}, func() error {
		rows, err := dbpool.Query(r.Context(), `with
	gg as (select p.usertype as usertype, g.id as gid, g.setting_base as measure
		from games as g
		join players as pc on g.id = pc.game
		join players as p on g.id = p.game
		join identities as i on i.id = p.identity
		join accounts as a on a.id = i.account
		where a.id = $1
		group by g.id, p.usertype
		order by g.id desc)

select usertype, measure, count(gid)
from gg
where usertype = any('{loser, winner, contender, fighter}')
group by measure, usertype
order by measure, usertype`, accountID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var measure, gameCount int
		var userType string
		_, err = pgx.ForEachRow(rows, []any{&userType, &measure, &gameCount}, func() error {
			switch userType {
			case "loser":
				ChartGamesByBaselevel.appendToColumn(fmt.Sprintf(`<img class="icons icons-base%d">`, measure), "Lost", chartSCcolorLost, gameCount)
			case "winner":
				ChartGamesByBaselevel.appendToColumn(fmt.Sprintf(`<img class="icons icons-base%d">`, measure), "Won", chartSCcolorWon, gameCount)
			}
			return nil
		})
		return err
	}, func() error {
		rows, err := dbpool.Query(r.Context(), `with
	gg as (select p.usertype as usertype, g.id as gid, g.setting_scavs as measure
		from games as g
		join players as pc on g.id = pc.game
		join players as p on g.id = p.game
		join identities as i on i.id = p.identity
		join accounts as a on a.id = i.account
		where a.id = $1
		group by g.id, p.usertype
		order by g.id desc)

select usertype, measure, count(gid)
from gg
where usertype = any('{loser, winner, contender, fighter}')
group by measure, usertype
order by measure, usertype`, accountID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var measure, gameCount int
		var userType string
		_, err = pgx.ForEachRow(rows, []any{&userType, &measure, &gameCount}, func() error {
			switch userType {
			case "loser":
				ChartGamesByScav.appendToColumn(fmt.Sprintf(`<img class="icons icons-scav%d">`, measure), "Lost", chartSCcolorLost, gameCount)
			case "winner":
				ChartGamesByScav.appendToColumn(fmt.Sprintf(`<img class="icons icons-scav%d">`, measure), "Won", chartSCcolorWon, gameCount)
			}
			return nil
		})
		return err
	}, func() error {
		rows, err := dbpool.Query(r.Context(), `with
	gg as (select p.usertype as usertype, g.id as gid, g.setting_alliance as measure, count(pc) as playercount
		from games as g
		join players as pc on g.id = pc.game
		join players as p on g.id = p.game
		join identities as i on i.id = p.identity
		join accounts as a on a.id = i.account
		where a.id = $1
		group by g.id, p.usertype
		order by g.id desc)

select usertype, measure, count(gid)
from gg
where usertype = any('{loser, winner, contender, fighter}') and playercount > 3
group by measure, usertype
order by measure, usertype`, accountID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		var measure, gameCount int
		var userType string
		_, err = pgx.ForEachRow(rows, []any{&userType, &measure, &gameCount}, func() error {
			switch userType {
			case "loser":
				ChartGamesByAlliances.appendToColumn(fmt.Sprintf(`<img class="icons icons-alliance%d">`, templatesAllianceToClassI(measure)), "", chartSCcolorLost, gameCount)
			case "winner":
				ChartGamesByAlliances.appendToColumn(fmt.Sprintf(`<img class="icons icons-alliance%d">`, templatesAllianceToClassI(measure)), "", chartSCcolorWon, gameCount)
			}
			return nil
		})
		return err
	})
	if DBErr(w, r, err) {
		return
	}

	basicLayoutLookupRespond("account", w, r, map[string]any{
		"and":                          and,
		"claimedIdentities":            claimedIdentities,
		"primaryDisplayName":           primaryDisplayName,
		"primaryClearName":             primaryClearName,
		"requestedClearName":           requestedClearName,
		"ChartGamesByPlayercount":      ChartGamesByPlayercount.calcTotals(),
		"ChartGamesByBaselevel":        ChartGamesByBaselevel.calcTotals(),
		"ChartGamesByAlliances":        ChartGamesByAlliances.calcTotals(),
		"ChartGamesByScav":             ChartGamesByScav.calcTotals(),
		"ResearchClassificationTotal":  ResearchClassificationTotal,
		"ResearchClassificationRecent": ResearchClassificationRecent,
	})
}

type eloHist struct {
	Rating int
}

func getRatingHistory(pid int) (map[string]eloHist, error) {
	rows, derr := dbpool.Query(context.Background(),
		`SELECT
			id,
			coalesce(ratingdiff, '{0,0,0,0,0,0,0,0,0,0,0}'),
			to_char(timestarted, 'YYYY-MM-DD HH24:MI'),
			players
		FROM games
		where
			array[$1::int] <@ players
			AND finished = true
			AND calculated = true
			AND hidden = false
			AND deleted = false
		order by timestarted asc`, pid)
	if derr != nil {
		if derr == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, derr
	}
	defer rows.Close()
	h := map[string]eloHist{}
	prevts := ""
	for rows.Next() {
		var gid int
		var rdiff []int
		var timestarted string
		var players []int
		err := rows.Scan(&gid, &rdiff, &timestarted, &players)
		if err != nil {
			return nil, err
		}
		k := -1
		for i, p := range players {
			if p == pid {
				k = i
				break
			}
		}
		if k < 0 || k >= len(rdiff) {
			log.Printf("Game %d is broken (k %d) players %v diffs %v", gid, k, players, rdiff)
			continue
		}
		rDiff := rdiff[k]
		if prevts == "" {
			h[timestarted] = eloHist{
				Rating: 1400 + rDiff,
			}
		} else {
			ph := h[prevts]
			h[timestarted] = eloHist{
				Rating: ph.Rating + rDiff,
			}
		}
		prevts = timestarted
	}
	return h, nil
}

func APIgetElodiffChartPlayer(_ http.ResponseWriter, r *http.Request) (int, any) {
	params := mux.Vars(r)
	pid, err := strconv.Atoi(params["pid"])
	if err != nil {
		return 400, nil
	}
	h, err := getRatingHistory(pid)
	if err != nil {
		return 500, err
	}
	return 200, h
}
