package server

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/nlopes/slack"
)

const (
	cookie  = ""
	MenuUrl = "https://munchery.com/menus/sf/#/0/dinner"
)

func getMenu(w http.ResponseWriter, r *http.Request) {
	_, err := http.NewRequest("GET", MenuUrl, nil)
	if err != nil {
	}
	io.WriteString(w, "Hello world!")
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
					/// HOW TO RESPOND
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
