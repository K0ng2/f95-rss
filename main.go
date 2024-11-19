package main

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
	_ "modernc.org/sqlite"
)

const BASE_API = "https://f95zone.to/sam/latest_alpha/latest_data.php?cmd=list&cat=games"

var (
	DBFILE  = os.Getenv("F95_RSS_DB")
	IDFILE  = os.Getenv("F95_RSS_ID_FILE") // id.txt file
	RSSCRON = os.Getenv("F95_RSS_CRON")
)

// RSS feed structures for XML serialization
type RSS struct {
	XMLName xml.Name `xml:"rss"`
	Version string   `xml:"version,attr"`
	Channel *Channel `xml:"channel"`
}

type Channel struct {
	Title       string  `xml:"title"`
	Link        string  `xml:"link"`
	Description string  `xml:"description"`
	Items       []*Item `xml:"item"`
}

type Item struct {
	Title       string    `xml:"title"`
	Link        string    `xml:"link"`
	Description string    `xml:"description"`
	PubDate     time.Time `xml:"pubDate"`
}

type F95 struct {
	Status string `json:"status"`
	Msg    struct {
		Data []F95DATA `json:"data"`
	} `json:"msg"`
}

type F95DATA struct {
	ThreadID int    `json:"thread_id"`
	Title    string `json:"title"`
	Creator  string `json:"creator"`
	Version  string `json:"version"`
	// Views    int      `json:"views"`
	// Likes    int      `json:"likes"`
	Prefixes []int `json:"prefixes"`
	Tags     []int `json:"tags"`
	// Rating   int      `json:"rating"`
	Cover   string   `json:"cover"`
	Screens []string `json:"screens"`
	// Date     string   `json:"date"`
	// Watched  bool     `json:"watched"`
	// Ignored  bool     `json:"ignored"`
	// New      bool     `json:"new"`
}

// Read IDs from a plain text file, one per line
func readIDsFromFile(filePath string) ([]int, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var ids []int
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			id, err := strconv.Atoi(line) // Convert the line to an integer ID
			if err != nil {
				return nil, err
			}
			ids = append(ids, id)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return ids, nil
}

// Function to fetch data from the database based on the list of IDs
func fetchDataFromDB(db *sql.DB, ids []int) ([]*Item, error) {
	var items []*Item

	// Loop through each ID and execute a query for each one
	for _, id := range ids {
		gameQuery := "SELECT id, title, version, updated FROM game WHERE id = ?"
		game := db.QueryRow(gameQuery, id)

		var gameID int
		var title, version, updated string

		// Fetch data from the row
		err := game.Scan(&gameID, &title, &version, &updated)
		if err != nil {
			if err == sql.ErrNoRows {
				// If no rows are returned, skip this ID
				continue
			}
			return nil, err
		}

		var coverURL string
		coverQuery := "select url from cover where game_id = ? order by id desc limit 1;"
		err = db.QueryRow(coverQuery, gameID).Scan(&coverURL)
		if err != nil {
			log.Fatalf("Failed to get the coverURL of id %d: %v", gameID, err)
		}

		link := fmt.Sprintf("https://f95zone.to/threads/%d", gameID)

		t, err := time.Parse(time.RFC3339, updated)
		if err != nil {
			log.Fatalf("Error parsing time: %v", err)
		}

		// Create a feed item and add it to the list
		item := &Item{
			Title:       fmt.Sprintf("%s [%s]", title, version),
			Link:        link,
			Description: "<img src=\"" + coverURL + "\" alt=\"" + title + "\" />",
			PubDate:     t.Local(),
		}
		items = append(items, item)
	}

	return items, nil
}

// Generate RSS feed with selected IDs
func generateFeed(db *sql.DB, ids []int) (*RSS, error) {
	channel := &Channel{
		Title:       "F95zone Latest Updates",
		Link:        "https://f95zone.com/latest",
		Description: "F95zone Adult Games - Latest Updates RSS Feed",
	}

	items, err := fetchDataFromDB(db, ids)
	if err != nil {
		return nil, err
	}

	channel.Items = items

	return &RSS{
		Version: "2.0",
		Channel: channel,
	}, nil
}

// Serve RSS feed
func serveFeed(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Read IDs from file
		ids, err := readIDsFromFile(IDFILE)
		if err != nil {
			http.Error(w, "Error reading IDs from file", http.StatusInternalServerError)
			return
		}

		feed, err := generateFeed(db, ids)
		if err != nil {
			http.Error(w, "Error generating feed", http.StatusInternalServerError)
			return
		}

		// Marshal the RSS feed into XML
		rssXML, err := xml.MarshalIndent(feed, "", "  ")
		if err != nil {
			http.Error(w, "Error converting feed to XML", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/xml")
		w.Write(rssXML)
	}
}

func getData() (data F95) {
	req, err := http.Get(BASE_API)
	if err != nil {
		log.Fatalf("Failed to create table: %v", err)
	}

	defer req.Body.Close()

	if err = json.NewDecoder(req.Body).Decode(&data); err != nil {
		log.Fatalf("Failed to create table: %v", err)
	}

	return data
}

func updateDatabase(db *sql.DB) {
	data := getData()
	for _, f := range data.Msg.Data {
		creatorID := insertCreator(db, f.Creator)
		insertGame(db, f.ThreadID, f.Title, f.Version, creatorID)
		insertCover(db, f.ThreadID, f.Cover)
		insertPreview(db, f.ThreadID, f.Screens)
		insertTags(db, f.ThreadID, f.Tags)
		insertPrefixes(db, f.ThreadID, f.Prefixes)
	}
	log.Println("Update successfully")
}

func insertCreator(db *sql.DB, creator string) int {
	var id int
	query := `
		INSERT INTO creator (name)
		VALUES (?)
		ON CONFLICT (name) DO UPDATE SET name = name
		RETURNING id;
	`

	err := db.QueryRow(query, creator).Scan(&id)
	if err != nil {
		log.Fatalf("failed to insert creator: %v", err)
	}

	return id
}

func insertGame(db *sql.DB, id int, title string, version string, creatorId int) {
	query := `
		insert into game (
			id, title, version, creator_id
		) values (?, ?, ?, ?)
		on conflict (id) do update set
			title = excluded.title,
			version = excluded.version,
			creator_id = excluded.creator_id
		;
	`

	_, err := db.Exec(query, id, title, version, creatorId)
	if err != nil {
		log.Fatalf("failed to insert game: %v", err)
	}
}

func insertCover(db *sql.DB, gameID int, coverURL string) {
	query := `insert or ignore into cover (url, game_id) values (?, ?);`

	_, err := db.Exec(query, coverURL, gameID)
	if err != nil {
		log.Fatalf("failed to insert cover: %v", err)
	}
}

func insertPreview(db *sql.DB, gameID int, previewURL []string) {
	query := `insert or ignore into preview (url, game_id) values (?, ?);`

	for _, s := range previewURL {
		_, err := db.Exec(query, s, gameID)
		if err != nil {
			log.Fatalf("failed to insert preview: %v", err)
		}
	}
}

func insertTags(db *sql.DB, gameID int, Tags []int) {
	query := `insert or ignore into tags (game_id, tag_id) values (?, ?);`

	for _, s := range Tags {
		_, err := db.Exec(query, gameID, s)
		if err != nil {
			log.Fatalf("failed to insert Tags: %v", err)
		}
	}
}

func insertPrefixes(db *sql.DB, gameID int, Prefixes []int) {
	query := `insert or ignore into prefixes (game_id, prefix_id) values (?, ?);`

	for _, s := range Prefixes {
		_, err := db.Exec(query, gameID, s)
		if err != nil {
			log.Fatalf("failed to insert Prefixes: %v", err)
		}
	}
}

func createDatabase(dbFile string) {
	file, err := os.Create(dbFile)
	if err != nil {
		log.Fatalf("Failed to create database file: %v", err)
	}
	file.Close()

	db, err := sql.Open("sqlite", dbFile)
	if err != nil {
		log.Fatalf("Failed to open the new database: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`
		create table if not exists creator (
			id integer primary key AUTOINCREMENT,
			name text not null unique
		);

		create table if not exists game (
			id integer primary key,
			title text not null,
			version text,
			created timestamp default (datetime(current_timestamp, 'localtime')),
			updated timestamp default (datetime(current_timestamp, 'localtime')),
			creator_id integer,
			foreign key(creator_id) references creator(id)
		);

		create table if not exists cover (
			id integer primary key autoincrement,
			url text not null unique,
			game_id integer,
			foreign key(game_id) references game(id)
		);

		create table if not exists preview (
			id integer primary key autoincrement,
			url text not null unique,
			game_id integer,
			foreign key(game_id) references game(id)
		);

		create table if not exists tags (
			game_id integer,
			tag_id integer,
			PRIMARY KEY(game_id, tag_id),
			foreign key(game_id) references game(id)
		);

		create table if not exists prefixes (
			game_id integer,
			prefix_id integer,
			PRIMARY KEY(game_id, prefix_id),
			foreign key(game_id) references game(id)
		);

		create trigger if not exists update_timestamp
		after update on game
		for each row
		begin
			update game
			set updated = current_timestamp
			where id = old.id;
		end;
	`)
	if err != nil {
		log.Fatalf("Failed to create table: %v", err)
	}

	log.Println("Database and tables created successfully.")
}

func main() {
	// Check if the database file exists
	if _, err := os.Stat(DBFILE); err != nil {
		if os.IsNotExist(err) {
			log.Println("Database file does not exist, creating it...")
			createDatabase(DBFILE)
		} else {
			log.Fatalf("Error checking database file: %v", err)
		}
	} else {
		log.Println("Database file already exists.")
	}

	db, err := sql.Open("sqlite", DBFILE)
	if err != nil {
		log.Fatalf("Failed to open the database: %v", err)
	}
	defer db.Close()

	// Start HTTP server to serve the feed
	http.HandleFunc("/feed", serveFeed(db))

	c := cron.New()

	c.AddFunc(RSSCRON, func() {
		updateDatabase(db)
		ids, err := readIDsFromFile(IDFILE) // Read IDs from file every 30 minutes
		if err != nil {
			log.Fatalf("Error reading IDs: %v", err)
		}
		_, err = generateFeed(db, ids)
		if err != nil {
			log.Println("Error generating feed:", err)
		}
	})

	c.Start()

	log.Println("Serving feed on http://localhost:8080/feed")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
