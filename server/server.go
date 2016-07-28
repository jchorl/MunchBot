package server

import (
	"log"
	"net/http"

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
	http.ListenAndServe(":8080", nil)
}
