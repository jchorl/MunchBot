package server

import (
	"fmt"
	"io"
	"net/http"
	"os"

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
	http.ListenAndServe(":8080", nil)
}

func ConnectToSlack() *slack.Client {
	token := os.Getenv("SLACK_TOKEN")
	api := slack.New(token)
	return api
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
