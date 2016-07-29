package server

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/nlopes/slack"
	"github.com/yhat/scrape"
	"golang.org/x/net/html"
)

const (
	session_id = ""
	MenuUrl    = "https://munchery.com/menus/sf/"
	MenuClass  = "menu-page-data"
)

func getMenu(w http.ResponseWriter, r *http.Request) {
	req, err := http.NewRequest("GET", MenuUrl, nil)
	if err != nil {
		log.Printf("Error creating request to get menu: %+v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	req.AddCookie(&http.Cookie{Name: "_session_id", Value: session_id})
	req.Header.Add("Accept", "*/*")
	req.Header.Add("User-Agent", "curl/7.43.0")

	client := http.DefaultClient
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error making request to get menu: %+v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()
	root, err := html.Parse(resp.Body)
	if err != nil {
		log.Printf("Error parsing body: %+v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	menu, ok := scrape.Find(root, scrape.ByClass(MenuClass))
	if !ok {
		log.Printf("Could not scrape properly")
		http.Error(w, "Could not scrape properly", http.StatusInternalServerError)
		return
	}

	log.Printf("%s", scrape.Text(menu))
}

func Run() {
	http.HandleFunc("/menu", getMenu)
	go http.ListenAndServe(":8080", nil)
	api := ConnectToSlack()
	SendTestMessage(api, "#intern-hackathon", "Just listening in...")
	//Respond(api)
	atMB := GetAtMunchBotId(api)
	Respond(api, atMB)
}

func ConnectToSlack() *slack.Client {
	token := os.Getenv("SLACK_TOKEN")
	api := slack.New(token)
	return api
}

func GetAtMunchBotId(api *slack.Client) string {
	users, _ := api.GetUsers()
	for _, user := range users {
		if user.IsBot && user.Name == "munchbot" {
			return "<@" + user.ID + ">"
		}
	}
	return "Couldn't find munchbot"
}

func SendTestMessage(api *slack.Client, channelName string, messageText string) {
	params := slack.PostMessageParameters{}
	channelID, timestamp, err := api.PostMessage(channelName, messageText, params)
	if err != nil {
		fmt.Printf("%s\n", err)
		return
	}
	fmt.Printf("Message successfully sent to channel %s at %s", channelID, timestamp)
}

func Respond(api *slack.Client, atBot string) {
	rtm := api.NewRTM()
	go rtm.ManageConnection()
	for {
		select {
		case msg := <-rtm.IncomingEvents:
			switch ev := msg.Data.(type) {
			case *slack.MessageEvent:
				fmt.Printf("Message: %v\n", ev)
				if strings.Contains(ev.Text, atBot) {
					switch {
					case strings.Contains(ev.Text, "Order"):
						params := slack.PostMessageParameters{}
						api.PostMessage(ev.Channel, "Ordering right now...", params)
					case strings.Contains(ev.Text, "love"):
						params := slack.PostMessageParameters{}
						api.PostMessage(ev.Channel, "Awww, thanks. Love you too, dawg.", params)
					default:
						params := slack.PostMessageParameters{}
						api.PostMessage(ev.Channel, "Come again? Didn't catch that", params)
					}
				}
			case *slack.RTMError:
				fmt.Printf("Error: %s\n", ev.Error())
			default:
				// Ignore other events..
				// fmt.Printf("Unexpected: %v\n", msg.Data)
			}
		}
	}
}
