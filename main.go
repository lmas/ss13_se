package ss13_se

import (
	"html/template"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/mux"
)

const (
	// Used internally for logging a global # of players
	internalServerTitle string = "_ss13.se"

	// How old a server entry can get, without updates, before it get's deleted
	oldServerTimeout = 24 * 3 // in hours
)

type Conf struct {
	// Web stuff
	WebAddr      string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration

	// Scraper stuff
	ScrapeTimeout time.Duration

	// Misc.
	Storage Storage
}

type App struct {
	conf      Conf
	web       *http.Server
	store     Storage
	templates map[string]*template.Template
	hub       ServerEntry // TODO: probably needs to be protected with a lock
}

func New(c Conf) (*App, error) {
	templates, err := loadTemplates()
	if err != nil {
		return nil, err
	}

	w := &http.Server{
		Addr:         c.WebAddr,
		ReadTimeout:  c.ReadTimeout,
		WriteTimeout: c.WriteTimeout,
	}

	a := &App{
		conf:      c,
		web:       w,
		store:     c.Storage,
		templates: templates,
	}

	r := mux.NewRouter()
	r.Handle("/", handler(a.pageIndex))
	r.Handle("/static/style.css", handler(a.pageStyle))
	r.Handle("/server/{id}", handler(a.pageServer))
	r.Handle("/server/{id}/daily", handler(a.pageDailyChart))
	r.Handle("/server/{id}/weekly", handler(a.pageWeeklyChart))
	r.Handle("/server/{id}/averagedaily", handler(a.pageAverageDailyChart))
	r.Handle("/server/{id}/averagehourly", handler(a.pageAverageHourlyChart))
	a.web.Handler = r

	return a, nil
}

func (a *App) Log(msg string, args ...interface{}) {
	log.Printf(msg+"\n", args...)
}

func (a *App) Run() error {
	a.Log("Opening storage...")
	err := a.store.Open()
	if err != nil {
		return err
	}

	webClient := &http.Client{
		Timeout: 60 * time.Second,
	}

	a.Log("Running updater")
	go a.runUpdater(webClient)

	a.Log("Running server on %s", a.conf.WebAddr)
	return a.web.ListenAndServe()
}

func (a *App) runUpdater(webClient *http.Client) {
	for {
		now := time.Now()
		servers, err := scrapeByond(webClient, now)
		dur := time.Since(now)
		if err != nil {
			a.Log("Scrape done in %s, errors: %v", dur, err)
		}

		if err == nil {
			servers = append(servers, a.makeHubEntry(now, servers))

			if err := a.store.SaveServers(servers); err != nil {
				a.Log("Error saving servers: %s", err)
			}

			if err := a.updateHistory(now, servers); err != nil {
				a.Log("Error saving server history: %s", err)
			}

			if err := a.updateOldServers(now); err != nil {
				a.Log("Error updating old servers: %s", err)
			}
		}

		time.Sleep(a.conf.ScrapeTimeout)
	}
}

func (a *App) updateHistory(t time.Time, servers []ServerEntry) error {
	var history []ServerPoint
	for _, s := range servers {
		history = append(history, ServerPoint{
			Time:     t,
			ServerID: s.ID,
			Players:  s.Players,
		})
	}
	return a.store.SaveServerHistory(history)
}

func (a *App) updateOldServers(t time.Time) error {
	servers, err := a.store.GetServers()
	if err != nil {
		return err
	}

	var remove []ServerEntry
	var update []ServerEntry
	for _, s := range servers {
		delta := t.Sub(s.Time)
		switch {
		case delta.Hours() > oldServerTimeout:
			remove = append(remove, s)
		case !s.Time.Equal(t):
			s.Players = 0
			update = append(update, s)
		}
	}

	if len(remove) > 0 {
		if err := a.store.RemoveServers(remove); err != nil {
			return err
		}
	}

	if len(update) > 0 {
		if err := a.store.SaveServers(update); err != nil {
			return err
		}
		if err := a.updateHistory(t, update); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) makeHubEntry(t time.Time, servers []ServerEntry) ServerEntry {
	var totalPlayers int
	for _, s := range servers {
		totalPlayers += s.Players
	}

	a.hub = ServerEntry{
		ID:      makeID(internalServerTitle),
		Title:   internalServerTitle,
		SiteURL: "",
		GameURL: "",
		Time:    t,
		Players: totalPlayers,
	}
	return a.hub
}
