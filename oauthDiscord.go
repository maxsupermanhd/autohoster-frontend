package main

import (
	"context"
	"encoding/json"
	"fmt"
	_ "fmt"
	"io"
	"log"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/davecgh/go-spew/spew"
	discord "github.com/ravener/discord-oauth2"
	"golang.org/x/oauth2"
)

var DiscordRedirectUrl = "https://wz2100-autohost.net/oauth/discord"

var discordOauthConfig = &oauth2.Config{
	RedirectURL: DiscordRedirectUrl,
	Scopes: []string{
		"connections", "identify", "guilds", "email"},
	Endpoint: discord.Endpoint,
}

// type DiscordUser struct {
// 	ID            string `json:"id"`
// 	Avatar        string `json:"avatar"`
// 	Username      string `json:"username"`
// 	Discriminator string `json:"discriminator"`
// }

func DiscordVerifyEnv() {
	var ok bool
	discordOauthConfig.ClientID, ok = cfg.GetString("discord", "id")
	if !ok {
		log.Println("Discord client ID not set")
	}
	discordOauthConfig.ClientSecret, ok = cfg.GetString("discord", "secret")
	if !ok {
		log.Println("Discord client secret not set")
	}
}

func DiscordGetUrl(state string) string {
	return discordOauthConfig.AuthCodeURL(state, oauth2.AccessTypeOffline)
}

func DiscordGetUInfo(token *oauth2.Token) map[string]any {
	res, err := discordOauthConfig.Client(context.Background(), token).Get("https://discord.com/api/accounts/@me")
	if err != nil {
		log.Printf("Unauthorized, resetting discord (%s)", spew.Sprintln(err))
		token.AccessToken = ""
		token.RefreshToken = ""
		token.Expiry = time.Now()
		return map[string]any{"DiscordError": "Error"}
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return map[string]any{"DiscordError": err.Error()}
	}
	jsonMap := make(map[string]any)
	err = json.Unmarshal([]byte(body), &jsonMap)
	if err != nil {
		log.Println(err.Error())
	}
	return jsonMap
}

func DiscordCallbackHandler(w http.ResponseWriter, r *http.Request) {
	if !sessionManager.Exists(r.Context(), "UserAuthorized") || sessionManager.Get(r.Context(), "UserAuthorized") != "True" {
		log.Println("Not authorized")
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": 1, "msg": "Not authorized"})
		return
	}
	if !sessionManager.Exists(r.Context(), "User.Username") {
		log.Println("Not authorized (no username)")
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": 1, "msg": "Not authorized (no username)"})
		return
	}
	code := r.FormValue("code")
	if sessionManager.Get(r.Context(), "User.Discord.State") != r.FormValue("state") {
		log.Println("Code missmatch")
		st := sessionManager.GetString(r.Context(), "User.Discord.State")
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": 1, "msg": "State missmatch " + st})
		return
	}
	token, err := discordOauthConfig.Exchange(r.Context(), code)
	if err != nil {
		log.Printf("Code exchange failed with error %s\n", err.Error())
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": 1, "msg": "Code exchange failed with error: " + err.Error()})
		return
	}
	if !token.Valid() {
		log.Println("Retreived invalid token")
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": 1, "msg": "Retreived invalid token"})
		return
	}
	tag, err := dbpool.Exec(r.Context(), "UPDATE accounts SET discord_token = $1, discord_refresh = $2, discord_refresh_date = $3 WHERE username = $4", token.AccessToken, token.RefreshToken, token.Expiry, sessionManager.Get(r.Context(), "User.Username"))
	if err != nil {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Something gone wrong, contact administrator."})
		notifyErrorWebhook(fmt.Sprintf("%s\n%s", err.Error(), string(debug.Stack())))
		return
	}
	if tag.RowsAffected() != 1 {
		basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msgred": true, "msg": "Something gone wrong, contact administrator."})
		notifyErrorWebhook(fmt.Sprintf("%s\n%s", tag.String(), string(debug.Stack())))
		return
	}
	log.Println("Got token")
	basicLayoutLookupRespond("plainmsg", w, r, map[string]any{"msggreen": 1, "msg": "Discord linked."})
}
