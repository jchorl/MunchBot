package server

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/nlopes/slack"
	"github.com/yhat/scrape"
	"golang.org/x/net/html"

	_ "github.com/lib/pq"
)

const (
	MenuUrl   = "https://munchery.com/menus/sf/"
	MenuClass = "menu-page-data"
)

func ConnectToPG(dbName string) *sql.DB {
	db, err := sql.Open("postgres", "postgres://munch:munch@"+os.Getenv("DB_PORT_5432_TCP_ADDR")+"/usertokens")
	if err != nil {
		log.Fatal(err)
	}
	return db
}

type MenuResp struct {
	Menu Menu `json:"menu"`
}

type Menu struct {
	MealServices MealService `json:"meal_services"`
}

type MealService struct {
	Dinner Meal `json:"dinner"`
}

type Meal struct {
	Sections []Section `json:"sections"`
}

type Section struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Items       []Item `json:"items"`
}

type Item struct {
	Availability string `json:"availability"`
	ID           int    `json:"id"`
	Name         string `json:"name"`
	Subtitle     string `json:"subtitle"`
	Description  string `json:"description"`
	Price        Price  `json:"price"`
	Photos       Photos `json:"photos"`
	URL          string `json:"url"`
}

type Price struct {
	Dollars int `json:"dollars,string"`
	Cents   int `json:"cents,string"`
}

type Photos struct {
	MenuSquare string `json:"menu_square"`
}

func menuHandler(muncherySession string, api *slack.Client) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		req, err := http.NewRequest("GET", MenuUrl, nil)
		if err != nil {
			log.Printf("Error creating request to get menu: %+v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		req.AddCookie(&http.Cookie{Name: "_session_id", Value: muncherySession})
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

		parsed := MenuResp{}
		err = json.Unmarshal([]byte(scrape.Text(menu)), &parsed)
		if err != nil {
			log.Printf("Error parsing body: %+v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		var mainDishes Section
		for _, section := range parsed.Menu.MealServices.Dinner.Sections {
			if section.Name == "Main Dishes" {
				mainDishes = section
				break
			}
		}

		attachments := make([]slack.Attachment, 0)
		for _, dish := range mainDishes.Items {
			if dish.Availability == "available" {
				attachments = append(attachments, slack.Attachment{
					Title:    dish.Name + ": ($" + strconv.Itoa(dish.Price.Dollars) + "." + strconv.Itoa(dish.Price.Cents) + ")",
					ThumbURL: dish.Photos.MenuSquare,
					Text:     dish.Description,
				})
			}
		}

		params := slack.PostMessageParameters{Text: "Dinner Options", Attachments: attachments}
		_, _, err = api.PostMessage("intern-hackathon", "", params)
		if err != nil {
			log.Printf("Error sending dishes: %+v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
}

func Run() {
	api := ConnectToSlack()
	SendTestMessage(api, "#intern-hackathon", "Just listening in...")
	//Respond(api)
	atMB := GetAtMunchBotId(api)
	go Respond(api, atMB)
	muncherySession := os.Getenv("MUNCHERY_SESSION")
	http.HandleFunc("/menu", menuHandler(muncherySession, api))
	http.ListenAndServe(":8080", nil)
}

func ConnectToSlack() *slack.Client {
	token := os.Getenv("SLACK_TOKEN")
	fmt.Println(token)
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
