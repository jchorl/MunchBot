package server

import (
	"io"
	"net/http"
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
