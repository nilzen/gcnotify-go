package main

import (
	"code.google.com/p/go-sqlite/go1/sqlite3"
	"code.google.com/p/go.net/html"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

type SettingsObject struct {
	GeocachingLat        float32
	GeocachingLng        float32
	GeocachingUserId     string
	GeocachingGspkUserId string
	PushoverUser         string
	PushoverToken        string
}

func main() {

	db, err := sqlite3.Open("./gcnotify.db")

	if err != nil {
		fmt.Printf("Database error: %v\n", err)
		os.Exit(1)
	}

	defer db.Close()

	createDatabaseSchema(db)

	settingsFile, err := ioutil.ReadFile("./settings.json")

	if err != nil {
		fmt.Printf("Settings file error: %v\n", err)
		os.Exit(1)
	}

	var settings SettingsObject
	json.Unmarshal(settingsFile, &settings)

	expiration := time.Now().AddDate(1, 0, 0)

	userIdCookie := http.Cookie{Name: "userid", Value: settings.GeocachingUserId, Expires: expiration}
	gspkUserIdCookie := http.Cookie{Name: "gspkuserid", Value: settings.GeocachingGspkUserId, Expires: expiration}

	url := fmt.Sprintf("http://www.geocaching.com/seek/nearest.aspx?lat=%v&lng=%v&ex=1", settings.GeocachingLat, settings.GeocachingLng)

	req, _ := http.NewRequest("GET", url, nil)
	req.AddCookie(&userIdCookie)
	req.AddCookie(&gspkUserIdCookie)

	jar, _ := cookiejar.New(nil)

	client := http.Client{Jar: jar}

	res, err := client.Do(req)

	if err != nil {
		log.Fatal(err)
		os.Exit(1)
	}

	defer res.Body.Close()

	tokenizer := html.NewTokenizer(res.Body)

	inCacheRow, inCacheLinkCol, inCacheLink := false, false, false
	foundCacheRow, isDisabledCache := false, false

	var currentTagName, currentCacheLink string

	for {
		tokenType := tokenizer.Next()
		switch tokenType {

		case html.ErrorToken:

			if !foundCacheRow {
				sendPush("No caches returned", "", settings)
			}

			return

		case html.StartTagToken:

			tagName, _ := tokenizer.TagName()

			currentTagName = string(tagName)

			if currentTagName == "tr" &&
				hasAttrVal(tokenizer, "class", "Data") {
				inCacheRow = true
				foundCacheRow = true
			}

			if inCacheRow && currentTagName == "td" &&
				hasAttrVal(tokenizer, "class", "Merge") {
				inCacheLinkCol = true

			}

			if inCacheLinkCol && currentTagName == "a" {
				inCacheLink = true
				currentCacheLink = getAttrVal(tokenizer, "href")
				isDisabledCache = hasAttrVal(tokenizer, "class", "Strike")
			}

		case html.EndTagToken:
			tagName, _ := tokenizer.TagName()

			if string(tagName) == "tr" && inCacheRow {
				inCacheRow = false
				inCacheLinkCol = false
				isDisabledCache = false
			}

			if string(tagName) == "a" {
				inCacheLink = false
			}

		case html.TextToken:

			if inCacheLink && currentTagName == "span" {
				text := tokenizer.Text()

				if !isDisabledCache && isNewCache(db, currentCacheLink, settings.GeocachingUserId) {
					notifyNewCache(db, currentCacheLink, string(text), settings)
				}
			}
		}
	}
}

func createDatabaseSchema(db *sqlite3.Conn) {

	query := "SELECT name FROM sqlite_master WHERE type='table' AND name='notifications';"
	_, err := db.Query(query)

	if err != nil {
		db.Exec("CREATE TABLE notifications (id INTEGER PRIMARY KEY AUTOINCREMENT, userid VARCHAR(36), url VARCHAR(256), title VARCHAR(256));")
	}
}

func isNewCache(db *sqlite3.Conn, url, userId string) bool {
	query := fmt.Sprintf("SELECT id FROM notifications WHERE userid='%s' AND url='%s';", userId, url)
	_, err := db.Query(query)

	return err != nil
}

func notifyNewCache(db *sqlite3.Conn, cacheUrl, title string, settings SettingsObject) {

	if sendPush(title, cacheUrl, settings) {
		sql := fmt.Sprintf("INSERT INTO notifications (url, title, userid) VALUES ('%s', '%s', '%s');", cacheUrl, title, settings.GeocachingUserId)

		db.Exec(sql)
	}
}

func sendPush(pushMessage, pushUrl string, settings SettingsObject) bool {

	data := url.Values{
		"token":   {settings.PushoverToken},
		"user":    {settings.PushoverUser},
		"message": {pushMessage},
		"url":     {pushUrl},
	}

	_, err := http.PostForm("https://api.pushover.net/1/messages.json", data)

	return err == nil
}

func getAttrVal(tokenizer *html.Tokenizer, attrName string) string {
	for {
		key, val, moreAttr := tokenizer.TagAttr()

		if string(key) == attrName {
			return string(val)
		}

		if !moreAttr {
			return ""
		}
	}
}

func hasAttrVal(tokenizer *html.Tokenizer, attrName, attrValue string) bool {
	val := getAttrVal(tokenizer, attrName)

	if val == "" {
		return false
	}

	fixedVal := " " + string(val) + " "
	fixedSearchVal := " " + attrValue + " "

	re := regexp.MustCompile("/[\t\r\n\f]/g")
	fixedSearchVal = re.ReplaceAllLiteralString(fixedSearchVal, " ")

	if strings.Index(fixedVal, fixedSearchVal) >= 0 {
		return true
	}

	return false
}
