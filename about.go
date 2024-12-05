package main

import (
	"io"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/maxsupermanhd/lac"
)

type backendConfiguredQueue struct {
	roomName string
	maps     map[string]string
}

func aboutHandler(w http.ResponseWriter, r *http.Request) {
	basicLayoutLookupRespond("about", w, r, map[string]any{
		"queues": fetchBackendQueues(),
	})
}

func fetchBackendQueues() (ret map[string]backendConfiguredQueue) {
	ret = map[string]backendConfiguredQueue{}
	cl := http.Client{
		Timeout: 1 * time.Second,
	}
	u, err := url.JoinPath(cfg.GetDSString("http://localhost:9271/", "backendUrl"), "config", "get")
	if err != nil {
		log.Println("Error getting backend url: ", err.Error())
		return
	}
	rsp, err := cl.Get(u)
	if err != nil {
		log.Println("Error fetching backend queues (get): ", err.Error())
		return
	}
	respBody, err := io.ReadAll(rsp.Body)
	if err != nil {
		log.Println("Error fetching backend queues (read): ", err.Error())
		return
	}
	c, err := lac.FromBytesJSON(respBody)
	if err != nil {
		log.Println("Error parsing backend config: ", err.Error())
		return
	}
	qns, ok := c.GetKeys("queues")
	if !ok {
		return
	}
	for _, qn := range qns {
		qdn, ok := c.GetString("queues", qn, "queueDisplayName")
		if !ok {
			continue
		}
		qrn, ok := c.GetString("queues", qn, "roomName")
		if !ok {
			continue
		}
		mns, ok := c.GetKeys("queues", qn, "maps")
		if !ok {
			continue
		}
		m := map[string]string{}
		for _, mn := range mns {
			mh, ok := c.GetString("queues", qn, "maps", mn, "hash")
			if !ok {
				continue
			}
			m[mn] = mh
		}
		ret[qdn] = backendConfiguredQueue{
			roomName: qrn,
			maps:     m,
		}
	}
	return
}
