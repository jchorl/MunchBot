package server

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
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
	MenuUrl     = "https://munchery.com/menus/sf/"
	CartUrl     = "https://munchery.com/api/cart/"
	CheckoutUrl = "https://munchery.com/checkout/"
	MenuClass   = "menu-page-data"
	CartDataId  = "cart_data"
)

var RegisteredChannels []string

func ConnectToPG(dbName string) *sql.DB {
	db, err := sql.Open("postgres", "postgres://munch:munch@"+os.Getenv("DB_PORT_5432_TCP_ADDR")+"/usertokens")
	if err != nil {
		log.Fatal(err)
	}
	return db
}

type CartResponse struct {
	Cart Cart `json:"cart"`
}

type Cart struct {
	ID         int                    `json:"id"`
	ItemsByDay map[string]interface{} `json:"items_by_day"`
}

type ItemByDay struct {
	Date string `json:"date"`
}

type AddToCartReq struct {
	ItemScheduleId int `json:"item_schedule_id"`
	Quantity       int `json:"qty"`
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

func getMenu(muncherySession string) (*html.Node, error) {
	req, err := http.NewRequest("GET", MenuUrl, nil)
	if err != nil {
		log.Printf("Error creating request to get menu: %+v", err)
		return nil, err
	}

	prepMuncheryReq(req, muncherySession)

	client := http.DefaultClient
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error making request to get menu: %+v", err)
		return nil, err
	}
	defer resp.Body.Close()
	root, err := html.Parse(resp.Body)
	if err != nil {
		log.Printf("Error parsing body: %+v", err)
		return nil, err
	}

	return root, nil
}

func parseMenu(root *html.Node) (*MenuResp, error) {
	menu, ok := scrape.Find(root, scrape.ByClass(MenuClass))
	if !ok {
		log.Printf("Could not scrape properly")
		return nil, errors.New("Could not scrape properly")
	}

	parsed := MenuResp{}
	err := json.Unmarshal([]byte(scrape.Text(menu)), &parsed)
	if err != nil {
		log.Printf("Error parsing body: %+v", err)
		return nil, err
	}

	return &parsed, nil
}

func menuHandler(muncherySession string, api *slack.Client) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		root, err := getMenu(muncherySession)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		parsed, err := parseMenu(root)
		if err != nil {
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

func ChannelExists(channelName string) bool {
	for _, channel := range RegisteredChannels {
		if channelName == channel {
			return true
		}
	}
	return false
}

func RegisterChannels(api *slack.Client) {
	RegisteredChannels = make([]string, 0)
	IMs, _ := api.GetIMChannels()
	for _, IM := range IMs {
		RegisteredChannels = append(RegisteredChannels, IM.ID)
	}
}

func isAvailable(meal Meal, id int) bool {
	for _, section := range meal.Sections {
		for _, item := range section.Items {
			if item.ID == id {
				return item.Availability == "Available"
			}
		}
	}

	return true
}

func addToBasket(muncherySession string, ids []int) error {
	// need to scrape for cart id
	root, err := getMenu(muncherySession)
	if err != nil {
		return err
	}

	menu, err := parseMenu(root)
	if err != nil {
		return err
	}

	// check that everything is available
	for _, id := range ids {
		if !isAvailable(menu.Menu.MealServices.Dinner, id) {
			return fmt.Errorf("Item id: %d is not available", id)
		}
	}

	cart, ok := scrape.Find(root, scrape.ById(CartDataId))
	if !ok {
		log.Printf("Could not scrape cart id properly")
		return err
	}

	parsed := CartResponse{}
	err = json.Unmarshal([]byte(scrape.Text(cart)), &parsed)
	if err != nil {
		log.Printf("Error parsing cart: %+v", err)
		return err
	}

	client := http.DefaultClient

	for _, id := range ids {
		body := AddToCartReq{
			ItemScheduleId: id,
			Quantity:       1,
		}
		bts, err := json.Marshal(body)
		if err != nil {
			log.Printf("Error marshaling body: %+v", err)
			return err
		}

		req, err := http.NewRequest("POST", CartUrl+"add", bytes.NewBuffer(bts))
		if err != nil {
			log.Printf("Error creating post to add to cart: %+v", err)
			return err
		}

		prepMuncheryReq(req, muncherySession)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			log.Printf("Error executing post to add to cart: %+v", err)
			return err
		}

		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			// TODO update with get menu command
			io.Copy(os.Stdout, resp.Body)
			return fmt.Errorf("Call to add to cart was unsuccessful. Please refresh the menu or hit up munchery.com")
		}

	}
	return nil
}

func setDeliveryWindow(muncherySession string, cartID int, date string) (io.ReadCloser, error) {
	req, err := http.NewRequest("POST", CartUrl+strconv.Itoa(cartID)+"/delivery_windows", bytes.NewBufferString(fmt.Sprintf("{\"delivery_windows\": {\"%s\": \"793\"}}", date)))
	if err != nil {
		return nil, err
	}

	prepMuncheryReq(req, muncherySession)
	req.Header.Set("Content-Type", "application/json")

	client := http.DefaultClient
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	return resp.Body, nil
}

func checkout(muncherySession string) error {
	req, err := http.NewRequest("GET", CheckoutUrl, nil)
	if err != nil {
		return err
	}

	prepMuncheryReq(req, muncherySession)

	client := http.DefaultClient
	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	root, err := html.Parse(resp.Body)
	if err != nil {
		log.Printf("Error parsing body for cart data: %+v", err)
		return err
	}

	cart, ok := scrape.Find(root, scrape.ById(CartDataId))
	if !ok {
		log.Printf("Could not scrape cart id properly")
		return err
	}

	log.Printf(scrape.Text(cart))
	parsed := CartResponse{}
	err = json.Unmarshal([]byte(scrape.Text(cart)), &parsed)
	if err != nil {
		log.Printf("Error parsing cart: %+v", err)
		return err
	}

	dates := make([]string, 0)
	// ItemsByDay is a map from string -> interface
	// as long as the key is not ordered_days, we can consider the struct an ItemByDay
	for k, v := range parsed.Cart.ItemsByDay {
		if k != "ordered_days" {
			ibd, ok := v.(map[string]interface{})
			if !ok {
				return fmt.Errorf("Could not cast key, value to map string->interface: %s, %+v", k, v)
			}

			date, ok := ibd["date"].(string)
			if !ok {
				return fmt.Errorf("Could not cast itemByDay date to string: %+v", ibd["date"])
			}

			dates = append(dates, date)
		}
	}

	updatedCart := []byte(scrape.Text(cart))

	// set delivery window
	// TODO handle delivernow
	for _, date := range dates {
		log.Printf("Setting delivery window for %s", date)
		updatedCartRC, err := setDeliveryWindow(muncherySession, parsed.Cart.ID, date)
		if err != nil {
			return err
		}

		defer updatedCartRC.Close()
		updatedCart, err = ioutil.ReadAll(updatedCartRC)
		if err != nil {
			log.Printf("Error reading updatedCartRC: %+v", updatedCartRC)
			return err
		}
	}

	req, err = http.NewRequest("POST", CartUrl+strconv.Itoa(parsed.Cart.ID)+"/checkout", bytes.NewBuffer(updatedCart))
	if err != nil {
		return err
	}

	prepMuncheryReq(req, muncherySession)
	req.Header.Set("Content-Type", "application/json")

	resp, err = client.Do(req)
	if err != nil {
		log.Printf("Error ordering from Munchery: %+v", err)
		return err
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Response was not ok. Please go to Munchery and figure out what's going on.")
	}

	return nil
}

func Run() {
	muncherySession := os.Getenv("MUNCHERY_SESSION")
	api := ConnectToSlack()
	RegisterChannels(api)
	SendTestMessage(api, "#intern-hackathon", "Just listening in...")
	atMB := GetAtMunchBotId(api)
	go Respond(api, atMB)
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
					case strings.Contains(strings.ToLower(ev.Text), "order"):
						if !ChannelExists(ev.Channel) {
							params := slack.PostMessageParameters{}
							api.PostMessage(ev.Channel, "Please order in a direct message ;)", params)
						} else {
							params := slack.PostMessageParameters{}
							ids := MakeOrder(ev.Text)
							addToBasket(muncherySessionID, ids)
							// processOrder()
							if order == nil {
								api.PostMessage(ev.Channel, "Sorry, didn't understand your order, format is '1, 2, 4' ;)", params)
								break
							}
							api.PostMessage(ev.Channel, "Ordering right now...", params)
						}
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

func MakeOrder(order string) []int {
	orders := strings.Split(order, ",")
	var orderNums []int
	for _, order := range orders {
		i, err := strconv.Atoi(order)
		if err != nil {
			log.Printf("Error processing your order... try again: %+v", err)
			return nil
		}
		orderNums = append(orderNums, i)
	}
	return orderNums
}

func prepMuncheryReq(req *http.Request, muncherySession string) {
	req.AddCookie(&http.Cookie{Name: "_session_id", Value: muncherySession})
	req.Header.Add("Accept", "*/*")
	req.Header.Add("User-Agent", "curl/7.43.0")
}
