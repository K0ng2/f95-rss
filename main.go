package main

import (
	"bufio"
	"database/sql"
	"encoding/xml"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const idFilePath = "f95_ids.txt" // Path to your text file containing the IDs

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
func fetchDataFromDB(ids []int) ([]*Item, error) {
	dbPath := os.Getenv("F95_DB_SQLITE")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

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
func generateFeed(ids []int) (*RSS, error) {
	channel := &Channel{
		Title:       "F95zone Latest Updates",
		Link:        "https://f95zone.com/latest",
		Description: "F95zone Adult Games - Latest Updates RSS Feed",
	}

	items, err := fetchDataFromDB(ids)
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
func serveFeed(w http.ResponseWriter, r *http.Request) {
	// Read IDs from file
	ids, err := readIDsFromFile(idFilePath)
	if err != nil {
		http.Error(w, "Error reading IDs from file", http.StatusInternalServerError)
		return
	}

	feed, err := generateFeed(ids)
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

func main() {
	// Start HTTP server to serve the feed
	http.HandleFunc("/feed", serveFeed)

	go func() {
		for {
			ids, err := readIDsFromFile(idFilePath) // Read IDs from file every 30 minutes
			if err != nil {
				log.Println("Error reading IDs:", err)
				continue
			}
			_, err = generateFeed(ids)
			if err != nil {
				log.Println("Error generating feed:", err)
			}
			time.Sleep(30 * time.Minute)
		}
	}()

	fmt.Println("Serving feed on http://localhost:8080/feed")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
