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
	"regexp"
	"strconv"
	"strings"

	"github.com/nlopes/slack"
	"github.com/robfig/cron"
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
var pg *sql.DB

func ConnectToPG(dbName string) *sql.DB {
	db, err := sql.Open("postgres", "postgres://munch:munch@"+os.Getenv("DB_PORT_5432_TCP_ADDR")+"/usertokens?sslmode=disable")
	if err != nil {
		log.Fatal(err)
	}
	return db
}

func SetupTable(db *sql.DB, tableName string) {
	_, err := db.Exec("CREATE TABLE users (channel text PRIMARY KEY, session text)")
	if err != nil {
		log.Printf("Error inserting into DB: %+v", err)
		return
	}
}

func RegisterUserToDB(db *sql.DB, user *User) {
	_, err := db.Exec("INSERT INTO users(channel,session) VALUES($1,$2) ON CONFLICT (channel) DO UPDATE SET (channel, session) = ($1, $2)", user.ChannelID, user.MuncherySession)
	if err != nil {
		log.Printf("Error inserting into DB: %+v", err)
		return
	}
}

func GetUser(db *sql.DB, channelID string) *User {
	var session string
	row := db.QueryRow("SELECT session FROM users WHERE channel = $1", channelID)
	err := row.Scan(&session)
	if err != nil {
		return nil
	}
	user := new(User)
	user.ChannelID = channelID
	user.MuncherySession = session
	return user
}

func GetUsers(db *sql.DB, api *slack.Client) (users []*User) {
	IMs, _ := api.GetIMChannels()
	for _, IM := range IMs {
		user := GetUser(db, IM.ID)
		if user != nil {
			users = append(users, user)
		}
	}
	return users
}

type User struct {
	ChannelID       string
	MuncherySession string
}

type CartResponse struct {
	Cart Cart `json:"cart"`
}

type Cart struct {
	ID         int                    `json:"id"`
	ItemsByDay map[string]interface{} `json:"items_by_day"`
	Total      float64
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

func checkConnection(muncherySession string) bool {
	req, err := http.NewRequest("GET", MenuUrl, nil)
	if err != nil {
		log.Printf("Error creating request to get menu: %+v", err)
	}

	prepMuncheryReq(req, muncherySession)

	client := http.DefaultClient
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error making request to get menu: %+v", err)
		return false
	}
	defer resp.Body.Close()
	root, err := html.Parse(resp.Body)
	if err != nil {
		log.Printf("Error parsing body: %+v", err)
		return false
	}
	_, err = parseMenu(root)
	if err != nil {
		return false
	}
	return true
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

func menuPost(muncherySession string, api *slack.Client, channelID string) error {
	root, err := getMenu(muncherySession)
	if err != nil {
		return err
	}

	parsed, err := parseMenu(root)
	if err != nil {
		return err
	}

	for _, section := range parsed.Menu.MealServices.Dinner.Sections {
		// nobody orders these things
		if section.Name == "Drinks" || section.Name == "Cooking Kits" || section.Name == "Kids" || section.Name == "Extras" {
			continue
		}

		attachments := make([]slack.Attachment, 0)
		for _, dish := range section.Items {
			if dish.Availability == "available" {
				attachments = append(attachments, slack.Attachment{
					Title:    dish.Name,
					ThumbURL: dish.Photos.MenuSquare,
					Fields: []slack.AttachmentField{
						slack.AttachmentField{
							Title: "ID",
							Value: strconv.Itoa(dish.ID),
							Short: true,
						},
						slack.AttachmentField{
							Title: "Price",
							Value: "$" + strconv.Itoa(dish.Price.Dollars) + "." + strconv.Itoa(dish.Price.Cents),
							Short: true,
						},
					},
					TitleLink: "https://munchery.com/menus/sf/#/0/dinner/" + dish.URL + "/info",
				})
			}
		}

		params := slack.PostMessageParameters{Attachments: attachments}
		_, _, err = api.PostMessage(channelID, section.Name, params)
		if err != nil {
			log.Printf("Error sending dishes: %+v", err)
			return err
		}
	}

	return nil
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
			return fmt.Errorf("Call to add to cart was unsuccessful. Please refresh the menu using `@munchbot menu` or hit up munchery.com.")
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

	// before final checkout, ensure cost is <$20
	parsedCart := CartResponse{}
	err = json.Unmarshal(updatedCart, &parsedCart)
	if err != nil {
		log.Printf("Could not unmarshal cart: %+v", err)
	}

	if parsedCart.Cart.Total > 20 {
		return fmt.Errorf("Cart total is >$20. Please fix it so you don't get charged.")
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

func RegisterCronJob(api *slack.Client, db *sql.DB) {
	c := cron.New()
	// gonna have to figure out timezones
	c.AddFunc("0 0 21 * * MON-FRI", func() { runCronPost(api, db) })
	c.Start()
}

func runCronPost(api *slack.Client, db *sql.DB) {
	users := GetUsers(db, api)
	for _, user := range users {
		go menuPost(user.MuncherySession, api, user.ChannelID)
	}
}

func Run() {
	//muncherySession := os.Getenv("MUNCHERY_SESSION")
	api := ConnectToSlack()
	RegisterChannels(api)
	pg = ConnectToPG("usertokens")
	SetupTable(pg, "users")
	SendTestMessage(api, "#intern-hackathon", "Just listening in...")
	atMB := GetAtMunchBotId(api)
	RegisterCronJob(api, pg)

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		runCronPost(api, pg)
	})
	go http.ListenAndServe(":8080", nil)

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
				RegisterChannels(api) //switch to change to a more efficient version
				params := slack.PostMessageParameters{}
				if ev.Msg.BotID == "" {
					switch {

					/* --------------- MENU CONVERSATION ---------------*/
					case strings.Contains(strings.ToLower(ev.Text), "menu") && !strings.Contains(strings.ToLower(ev.Text), "register"):
						if !ChannelExists(ev.Channel) {
							api.PostMessage(ev.Channel, "Please speak with `@munchbot` in your direct message channel with `@munchbot`", params)
						} else {
							user := GetUser(pg, ev.Channel)
							if user == nil {
								api.PostMessage(ev.Channel, "Hi, to use `@munchbot` type `register {munchery_cookie}` then `menu` to see the Munchery Menu of the day, followed by `order {menu item ids separated by comma}`", params)
							} else {
								api.PostMessage(ev.Channel, "Hey! Here's the menu:", params)
								menuPost(user.MuncherySession, api, ev.Channel)
							}
						}

					/* --------------- ORDER CONVERSATION ---------------*/
					case strings.Contains(strings.ToLower(ev.Text), "order") && !strings.Contains(strings.ToLower(ev.Text), "register"):
						if !ChannelExists(ev.Channel) {
							api.PostMessage(ev.Channel, "Please speak with `@munchbot` in your direct message channel with `@munchbot`", params)
						} else {
							ids, parseError := ParseOrder(ev.Text)
							if ids == nil || parseError {
								api.PostMessage(ev.Channel, "Sorry, didn't understand your order, format is `order 1, 2, 4`", params)
							} else {
								api.PostMessage(ev.Channel, "Hey we registered your order. It should arrive at around 6pm... sending you a confirmation email!", params)
								user := GetUser(pg, ev.Channel)
								addToBasket(user.MuncherySession, ids)
								checkout(user.MuncherySession)
							}
						}

					/* -------------- REGISTER CONVERSATION ---------------*/
					case strings.Contains(strings.ToLower(ev.Text), "register"):
						if !ChannelExists(ev.Channel) {
							params := slack.PostMessageParameters{}
							api.PostMessage(ev.Channel, "You must register in the private channel with @munchbot", params)
						} else {
							muncherySessionID, skip := ParseRegistration(ev.Text, api, ev.Channel) // TODO
							if skip {
								api.PostMessage(ev.Channel, "Sorry, the munchery token `"+muncherySessionID+"` was not valid", params)
								break
							}
							if !checkConnection(muncherySessionID) {
								api.PostMessage(ev.Channel, "Sorry, the munchery token `"+muncherySessionID+"` was not valid", params)
							} else {
								api.PostMessage(ev.Channel, "Perfect, registering you with @munchbot -- to make an order type `menu` or `order`", params)
								user := new(User)
								user.ChannelID = ev.Channel
								user.MuncherySession = muncherySessionID
								RegisterUserToDB(pg, user)
							}
						}

					/*  ------------------ NONE OF THE ABOVE ---------------- */
					default:
						if !ChannelExists(ev.Channel) {
							params := slack.PostMessageParameters{}
							api.PostMessage(ev.Channel, "You must register in the private channel with @munchbot", params)
						} else {
							params := slack.PostMessageParameters{}
							api.PostMessage(ev.Channel, "Hi, to use `@munchbot` type `register {munchery_cookie}` then `menu` to see the Munchery Menu of the day, followed by `order {menu item ids separated by comma}`", params)
						}
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

func ParseRegistration(messageBody string, api *slack.Client, channel string) (string, bool) {
	params := slack.PostMessageParameters{}
	registrationText := strings.Split(messageBody, " ")
	if len(registrationText) < 2 {
		api.PostMessage(channel, "Looks like you didn't get the format right... to register type `@munchbot register {MUNCHERY_COOKIE}", params)
		return "", false
	}
	if strings.ToLower(registrationText[0]) != "register" {
		api.PostMessage(channel, "Looks like you didn't get the format right... to register type `@munchbot register {MUNCHERY_COOKIE}", params)
		return "", true
	}
	var token string
	for i, strings := range registrationText {
		if i >= 1 {
			token = token + strings
		}
	}
	return token, false
}

func ParseOrder(order string) ([]int, bool) {
	orders := strings.Split(order, " ")
	var orderNums []int
	for j, order := range orders {
		if j == 0 {

		} else {
			order = strings.Replace(order, ",", "", -1)
			i, err := strconv.Atoi(order)
			if err != nil {
				return nil, true
			}
			orderNums = append(orderNums, i)
		}
	}
	return orderNums, false
}

func prepMuncheryReq(req *http.Request, muncherySession string) {
	req.AddCookie(&http.Cookie{Name: "_session_id", Value: muncherySession})
	req.Header.Add("Accept", "*/*")
	req.Header.Add("User-Agent", "curl/7.43.0")
}

func muncherySessionFromCookie(cookie string) (string, error) {
	var sessionRegexp *regexp.Regexp = regexp.MustCompile(`_session_id=(.*?);`)
	match := sessionRegexp.FindStringSubmatch(cookie)
	if len(match) < 2 {
		return "", fmt.Errorf("Not enough matches")
	}
	return match[1], nil
}
