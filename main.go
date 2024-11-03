package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/xml"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"
	"github.com/robfig/cron/v3"
	_ "modernc.org/sqlite"
)

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
		query := "SELECT * FROM game WHERE id = ?"
		row := db.QueryRow(query, id)

		var published int64
		var idStr, name, version, creator, imageUrl string

		// Fetch data from the row
		err := row.Scan(&idStr, &name, &version, &creator, &published, &imageUrl)
		if err != nil {
			if err == sql.ErrNoRows {
				// If no rows are returned, skip this ID
				continue
			}
			return nil, err
		}

		link := "https://f95zone.to/threads/" + idStr
		// Create a feed item and add it to the list
		item := &Item{
			Title:       fmt.Sprintf("%s [%s]", name, version),
			Link:        link,
			Description: "<img src=\"" + imageUrl + "\" alt=\"" + name + "\" />",
			PubDate:     time.Unix(published, 0),
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
		create table if not exists game (
			id integer primary key,
			name text,
			version text,
			creator text,
			published integer,
			imageUrl text
		);
	`)
	if err != nil {
		log.Fatalf("Failed to create table: %v", err)
	}

	log.Println("Database and tables created successfully.")
}

type Game struct {
	id        int
	name      string
	version   string
	creator   string
	publisher int64
	imageUrl  string
}

func getFeed() *gofeed.Feed {
	url := "https://f95zone.to/sam/latest_alpha/latest_data.php?cmd=rss&cat=games"
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	fp := gofeed.NewParser()
	feed, err := fp.ParseURLWithContext(url, ctx)
	if err != nil {
		log.Fatalf("Error checking database file: %v", err)
	}

	return feed
}

func parseData(data *gofeed.Item) *Game {
	parsedURL, err := url.Parse(data.Link)
	if err != nil {
		log.Println("Error parsing URL:", err)
	}

	id, err := strconv.Atoi(path.Base(parsedURL.Path))
	if err != nil {
		log.Printf("Error converting string to integer: %v\n", err)
	}

	re := regexp.MustCompile(`\] (?<name>.*?) \[(?<version>.+)\]`)
	matches := re.FindStringSubmatch(data.Title)
	name := re.SubexpIndex("name")
	version := re.SubexpIndex("version")
	return &Game{
		id:        id,
		name:      matches[name],
		version:   matches[version],
		creator:   data.DublinCoreExt.Creator[0],
		publisher: data.PublishedParsed.Unix(),
		imageUrl:  data.Image.URL,
	}
}

func updateDatabase(db *sql.DB) {
	feed := getFeed()
	for _, f := range feed.Items {
		stmt, err := db.Prepare("insert or replace into game values (?, ?, ?, ?, ?, ?)")
		if err != nil {
			log.Fatalf("failed to prepare statement: %v", err)
		}
		defer stmt.Close()

		data := parseData(f)
		_, err = stmt.Exec(data.id, data.name, data.version, data.creator, data.publisher, data.imageUrl)
		if err != nil {
			log.Fatalf("failed to insert game: %v", err)
		}
	}
	log.Println("Update successfully")
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
