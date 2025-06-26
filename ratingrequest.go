package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"github.com/jackc/pgx/v5"
)

type Ra struct {
	Dummy                 bool   `json:"dummy"`
	Autohoster            bool   `json:"autohoster"`
	Star                  [3]int `json:"star"`
	Medal                 int    `json:"medal"`
	Level                 int    `json:"level"`
	Elo                   string `json:"elo"`
	Details               string `json:"details"`
	Name                  string `json:"name"`
	Tag                   string `json:"tag"`
	NameTextColorOverride [3]int `json:"nameTextColorOverride"`
	TagTextColorOverride  [3]int `json:"tagTextColorOverride"`
	EloTextColorOverride  [3]int `json:"eloTextColorOverride"`
}

func ratingHandler(w http.ResponseWriter, r *http.Request) {
	params := mux.Vars(r)
	hash, ok := params["hash"]
	if !ok {
		hash = r.Header.Get("WZ-Player-Hash")
	}
	w.Header().Set("Content-Type", "application/json")
	if hash == "" {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("{\"error\": \"Empty hash.\"}"))
		return
	}
	m := ratingLookup(hash, r.Header.Get("WZ-Version"))
	j, err := json.Marshal(m)
	if err != nil {
		log.Println(err.Error())
	}
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, string(j))
}

func ratingLookup(hash string, gameVersion string) Ra {
	m := Ra{
		Star:                  [3]int{},
		NameTextColorOverride: [3]int{0xff, 0xff, 0xff},
		TagTextColorOverride:  [3]int{0xff, 0xff, 0xff},
		EloTextColorOverride:  [3]int{0xff, 0xff, 0xff},
	}
	ohash, ok := cfg.GetString("ratingOverrides", hash)
	if ok {
		hash = ohash
	}
	if hash == "a0c124533ddcaf5a19cc7d593c33d750680dc428b0021672e0b86a9b0dcfd711" {
		m.Autohoster = true
		var c int
		derr := dbpool.QueryRow(context.Background(), "select count(*) from games where hidden = false and deleted = false;").Scan(&c)
		if derr != nil {
			log.Print(derr)
		}
		m.Details = "wz2100-autohost.net\n\nTotal games served: " + strconv.Itoa(c) + "\n"
		m.Elo = "Visit wz2100-autohost.net"
		return m
	}

	var lid int
	var lacc *int
	var lnames []string
	var lmod, lexcludemod, lterm, ladmin, lhidden bool

	var lwinsZ *int
	var lwins int

	var lrating map[string]any

	err := dbpool.QueryRow(context.Background(), `
with
	s1 as (select identities.id as lid,
				identities.account as lacc,
				identities.rating_hidden as lhidden,
				identities.exclude_admin as lexcludemod,
				(select array_agg(display_name)
				from (select names.display_name
						from names
						where account = accounts.id and names.status = 'approved'
						order by rank () over (order by names.id = accounts.name desc))) as lnames,
				coalesce(accounts.allow_host_request, false) as lmod,
				coalesce(accounts.terminated, false) as lterm,
				coalesce(accounts.superadmin, false) as ladmin
			from identities
			left join accounts on accounts.id = identities.account
			where hash = $1),
	s2 as (select s1.lid as s2lid, coalesce(count(players), 0) as lwins
			from players, s1
			where players.usertype = 'winner' and players.identity = s1.lid
			group by s1.lid),
	s3 as (select json_build_object(
				'account', rating.account,
				'elo', rating.elo,
				'played', rating.played,
				'won', rating.won,
				'lost', rating.lost,
				't', 'elo') as r,
				rating.account as racc
			from rating, s1
			where rating.account = s1.lacc and rating.category = 2)
select lid, lacc, lnames, lmod, lterm, ladmin, lhidden, lexcludemod, lwins, r
from s1
left join s2 on s1.lid = s2.s2lid
left join s3 on s1.lacc = s3.racc`, hash).Scan(
		&lid, &lacc, &lnames, &lmod, &lterm, &ladmin, &lhidden, &lexcludemod,
		&lwinsZ,
		&lrating,
	)

	if lhidden {
		m.Details = "This identity decided to hide their name\n"
		m.NameTextColorOverride = [3]int{0x66, 0x66, 0x66}
		m.EloTextColorOverride = [3]int{0xff, 0x44, 0x44}
		m.Elo = "Unknown player"
		return m
	}

	if lwinsZ != nil {
		lwins = *lwinsZ
	}

	if err != nil {
		if err != pgx.ErrNoRows {
			m.Elo = "request failed"
			log.Print(err)
			return m
		}
		m.Details = "No information on that hash\n"
		m.NameTextColorOverride = [3]int{0x66, 0x66, 0x66}
		m.EloTextColorOverride = [3]int{0xff, 0x44, 0x44}
		m.Elo = "Unknown player"
		return m
	}

	if lacc == nil {
		m.Details = "Not registered player\n"
		m.NameTextColorOverride = [3]int{0x66, 0x66, 0x66}
		m.EloTextColorOverride = [3]int{0xff, 0x44, 0x44}
		m.Elo = fmt.Sprintf("Unknown player (% 4d wins)", lwins)
		return m
	}

	if lterm {
		m.Level = 0
		m.NameTextColorOverride = [3]int{0xff, 0x22, 0x22}
		m.EloTextColorOverride = [3]int{0xff, 0x22, 0x22}
		m.TagTextColorOverride = [3]int{0xff, 0x22, 0x22}
		m.Elo = "Account terminated"
		return m
	}

	if ladmin {
		m.Level = 8
		m.NameTextColorOverride = [3]int{0x33, 0xff, 0x33}
		m.Name = "Admin"
	} else if lmod && !lexcludemod {
		m.Level = 7
		m.NameTextColorOverride = [3]int{0x11, 0xaa, 0x11}
		m.Name = "Moderator"
	} else if len(lnames) > 0 {
		m.Level = 1
	}

	ratingtype, _ := lrating["t"].(string)
	switch ratingtype {
	default:
		m.Details = "Not participated in rated games\n"
		m.EloTextColorOverride = [3]int{0xbb, 0xbb, 0xbb}
		m.Elo = fmt.Sprintf("Not rated (% 4d wins)", lwins)
	case "elo":
		m.Details += fmt.Sprintf("Showing rating type %s (category 3)\n", ratingtype)

		relo := int(lrating["elo"].(float64))
		rplayed := int(lrating["played"].(float64))
		rwon := int(lrating["won"].(float64))
		rlost := int(lrating["lost"].(float64))

		m.Details += fmt.Sprintf("Rating: % 4d Played: % 4d\n", relo, rplayed)
		m.Details += fmt.Sprintf("Won: % 4d Lost: % 4d\n", rwon, rlost)

		pc := "-"
		if rwon+rlost > 0 {
			pc = fmt.Sprintf("%03.1f%%", float64(100)*(float64(rwon)/float64(rwon+rlost)))
		}

		m.Elo = fmt.Sprintf("R[% 4d] % 4d %s", relo, rplayed, pc)

		if rplayed < 5 {
			m.Dummy = true
		} else {
			m.Dummy = false
			if rlost == 0 {
				rlost = 1
			}
			if rwon >= 24 && float64(rwon)/float64(rlost) > 6.0 {
				m.Medal = 1
			} else if rwon >= 12 && float64(rwon)/float64(rlost) > 4.0 {
				m.Medal = 2
			} else if rwon >= 6 && float64(rwon)/float64(rlost) > 3.0 {
				m.Medal = 3
			}
			if relo > 1800 {
				m.Star[0] = 1
			} else if relo > 1550 {
				m.Star[0] = 2
			} else if relo > 1400 {
				m.Star[0] = 3
			}
			if rplayed > 60 {
				m.Star[1] = 1
			} else if rplayed > 30 {
				m.Star[1] = 2
			} else if rplayed > 10 {
				m.Star[1] = 3
			}
			if rwon > 60 {
				m.Star[2] = 1
			} else if rwon > 30 {
				m.Star[2] = 2
			} else if rwon > 10 {
				m.Star[2] = 3
			}
		}
	}

	if len(lnames) > 0 {
		m.Details += "Registered as:\n"
		m.Details += strings.Join(lnames, ", ")
	}

	return m
}
