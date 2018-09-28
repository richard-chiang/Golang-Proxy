package main

import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"
)

// go run web-cache.go [ip:port] [replacement_policy] [cache_size] [expiration_time]
// [ip:port] : The TCP IP address and the port at which the web-cache will be running.
// [replacement_policy] : The replacement policy ("LRU" or "LFU") that the web cache follows during eviction.
// [cache_size] : The capacity of the cache in MB (your cache cannot use more than this amount of capacity). Note that this specifies the (same) capacity for both the memory cache and the disk cache.
// [expiration_time] : The time period in seconds after which an item in the cache is considered to be expired.

type CacheEntry struct {
	RawData    []byte
	Dtype      string // "img/png" | "img/jpg" | "text/javascript" ....
	UseFreq    uint64 // # of access
	Header     http.Header
	CreateTime time.Time
	LastAccess time.Time
}

type UserOptions struct {
	EvictPolicy    string
	CacheSize      int
	ExpirationTime time.Duration
}

var options UserOptions
var CacheMutex *sync.Mutex
var MemoryCache map[string]CacheEntry

const CacheFolderPath string = "./cache/"

func main() {
	options = UserOptions{
		EvictPolicy:    "LFU",
		CacheSize:      10,
		ExpirationTime: time.Duration(10) * time.Second}

	// IpPort := os.Args[1] // send and receive data from Firefox
	// ReplacementPolicy := os.Args[2] // LFU or LRU or ELEPHANT
	// CacheSize := os.Args[3]
	// ExpirationTime := os.Args[4] // time period in seconds after which an item in the cache is considered to be expired

	IpPort := "localhost:1243"

	// if !(EvictPolicy == "LRU") && !(EvictPolicy == "LFU") && !(EvictPolicy == "ELEPHANT") {
	// 	fmt.Println("Please enter the proper evict policy: LFU or LRU only")
	// 	os.Exit(1)
	// }

	s := &http.Server{
		Addr: IpPort,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			HandlerForFireFox(w, r)
		}),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	MemoryCache = map[string]CacheEntry{}
	CacheMutex = &sync.Mutex{}
	RestoreCache()
	log.Fatal(s.ListenAndServe())

}

func HandlerForFireFox(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	if r.Method == "GET" {
		// Cache <- Response
		entry, existInCache := GetByURL(r.RequestURI)

		if !existInCache && options.EvictPolicy == "ELEPHANT" {
			entry, existInCache = GetFromDiskUrl(r.RequestURI)
		}

		if !existInCache {

			// call request to get data for caching
			resp := NewRequest(w, r)

			if resp == nil {
				return
			}

			if resp.StatusCode != 200 {
				ForwardResponseToFireFox(w, r)
			}

			// Create New Cache Entry
			data, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				fmt.Println("Something wrong while parsing data")
			}

			if strings.Contains(http.DetectContentType(data), "text/html") {
				newEntry := NewCacheEntry(data)
				newEntry.RawData = data
				newEntry.Header = http.Header{}
				for name, values := range resp.Header {
					for _, v := range values {
						newEntry.Header.Add(name, v)
					}
				}
				AddCacheEntry(r.RequestURI, newEntry)
				entry = newEntry
				//parseHTML2(r.RequestURI)
				ParseHTML(resp)
			}

			resp.Body.Close()
		}

		for name, values := range entry.Header {
			for _, v := range values {
				w.Header().Add(name, v)
			}
		}
		w.WriteHeader(200)
		_, err := io.Copy(w, bytes.NewReader(entry.RawData))

		CheckError("io copy", err)

	} else {
		ForwardResponseToFireFox(w, r)
	}

}

func ForwardResponseToFireFox(w http.ResponseWriter, r *http.Request) {
	// forward response to firefox
	resp := NewRequest(w, r)

	if resp == nil {
		return
	}

	defer resp.Body.Close()

	for name, values := range resp.Header {
		for _, v := range values {
			w.Header().Add(name, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	_, err := io.Copy(w, resp.Body)
	if err != nil {
		http.Error(w, "Internal Server Error", 500)
		DebugPrint("io.Copy error", "Issue with")
		panic(err)
	}
}

func parseHTML2(url string) {
	entry := ReadFromDisk(Encrypt(url))
	buf := entry.RawData
	///////// Modify html byte[]
	pageContent := string(buf)

	// extract anchor with src from html
	re := regexp.MustCompile("<.*?src=\".*?\".*?>")
	fmt.Println("regex")
	tagsWithSRC := re.FindAllString(pageContent, -1)

	// extract src from anchor, should be in order with tagsWithSRC
	listOfSrc := make([]string, len(tagsWithSRC))
	for i, tag := range tagsWithSRC {
		re = regexp.MustCompile("src=\".*?\"")
		src := re.FindAllString(tag, 1)[0]
		listOfSrc[i] = src
	}

	// extract url from src, should be in order with tagsWithSRC
	urls := make([]string, len(tagsWithSRC))
	for i, src := range listOfSrc {
		re = regexp.MustCompile("\".*?\"")
		url = re.FindAllString(src, 1)[0]
		url = url[1 : len(url)-1] // remove the first and last quotation mark
		urls[i] = url
	}

	for i := 0; i < len(tagsWithSRC); i++ {
		oldString := tagsWithSRC[i]
		fmt.Println(oldString)
		fmt.Println(urls[i])
		fmt.Println("====================")
		// strings.Replace(pageContent)
	}

	// for _, img := range listOfImg {
	// 	re := regexp.MustCompile("^<img.*src=\".*?\".*?>$")
	// 	fmt.Println("img")
	// 	fmt.Println(re.FindAllString(img, 2))
	// 	// strings.Replace(pageContent, img, )
	// }

	///////
	// resp.Body = ioutil.NopCloser(bytes.NewBuffer(buf))
	// resp.ContentLength = int64(len(buf))
}

func parseHTML3(resp *http.Response, url string) {
	fileName := Encrypt(url)
	outFile, err := os.Create(CacheFolderPath + fileName)
	CheckError("cannot create html file", err)

	defer outFile.Close()

	_, err = io.Copy(outFile, resp.Body)
	CheckError("cannot copy html to file", err)

}

func ParseHTML(resp *http.Response) {
	const LINK_TAG = "link"
	const IMG_TAG = "img"
	const SCRIPT_TAG = "script"

	cursor := html.NewTokenizer(resp.Body)

	for {
		token := cursor.Next()
		switch token {
		case html.ErrorToken:
			fmt.Println()
			return
		case html.StartTagToken:
			fetchedToken := cursor.Token()
			switch fetchedToken.Data {
			case LINK_TAG:
				for i, a := range fetchedToken.Attr {
					if a.Key == "href" {
						RequestResource(a)
						fetchedToken.Attr[i].Val = Encrypt(a.Val)
					}
				}
			case IMG_TAG:
				for i, a := range fetchedToken.Attr {
					if a.Key == "src" {
						RequestResource(a)
						fetchedToken.Attr[i].Val = Encrypt(a.Val)
					}
				}
			case SCRIPT_TAG:
				for i, a := range fetchedToken.Attr {
					if a.Key == "src" {
						RequestResource(a)
						fetchedToken.Attr[i].Val = Encrypt(a.Val)
					}
				}
			}
		}
	}
}

// ===========================================================
// ===========================================================
//					Helper for Cache Entry
// ===========================================================
// ===========================================================

func GetFromDiskHash(hashkey string) (CacheEntry, bool) {
	CacheMutex.Lock()
	defer CacheMutex.Unlock()

	files, err := filepath.Glob(CacheFolderPath + "*")
	CheckError("err restoring cache. Cannot fetch file names", err)

	for _, fileName := range files {
		fileName = strings.TrimPrefix(fileName, "cache/")

		if fileName == ".DS_Store" {
			continue
		}
		// If the file was found
		if fileName == hashkey {
			// Delete from memory if the cache is too big
			for len(MemoryCache) >= options.CacheSize {
				Evict()
			}
			MemoryCache[fileName] = ReadFromDisk(fileName)
			return MemoryCache[fileName], true
		}
	}

	return CacheEntry{}, false
}

func GetFromDiskUrl(url string) (CacheEntry, bool) {
	hashkey := Encrypt(url)
	return GetFromDiskHash(hashkey)
}

func GetByHash(hashkey string) (CacheEntry, bool) {
	CacheMutex.Lock()

	entry, exist := MemoryCache[hashkey]
	if exist {
		if isExpired(hashkey) {
			DeleteCacheEntry(hashkey)
			exist = false
		} else {
			entry.LastAccess = time.Now()
			entry.UseFreq++
		}
	}
	CacheMutex.Unlock()

	return entry, exist
}

func GetByURL(url string) (CacheEntry, bool) {
	hashkey := Encrypt(url)
	return GetByHash(hashkey)
}

// Fetch the img/link/script from the url provided in an html
func RequestResource(a html.Attribute) {
	resp, err := http.Get(a.Val)
	fmt.Println("url: " + a.Val)
	if err != nil {
		time.Sleep(time.Second)
		resp, err = http.Get(a.Val)
	}
	CheckError("request resource: get request", err)
	bytes, err := ioutil.ReadAll(resp.Body)
	CheckError("request resource: readall", err)
	entry := NewCacheEntry(bytes)
	entry.RawData = bytes
	AddCacheEntry(a.Val, entry)
}

// Fill in RawData
func NewCacheEntry(data []byte) CacheEntry {
	NewEntry := CacheEntry{}
	NewEntry.Dtype = http.DetectContentType(data)
	NewEntry.CreateTime = time.Now()
	NewEntry.LastAccess = time.Now()
	NewEntry.UseFreq = 1
	return NewEntry
}

// Atomic adding to the cache
func AddCacheEntry(URL string, entry CacheEntry) {
	CacheMutex.Lock()
	for len(MemoryCache) >= options.CacheSize {
		Evict()
	}

	fileName := Encrypt(URL)
	MemoryCache[fileName] = entry
	WriteToDisk(fileName, entry)
	CacheMutex.Unlock()
}

func WriteToDisk(fileHash string, entry CacheEntry) {
	bytes, err := json.Marshal(entry)
	CheckError("json marshal error", err)

	file, err := os.Create(CacheFolderPath + fileHash)
	CheckError("Create File Error", err)

	writer := bufio.NewWriter(file)
	writer.Write(bytes)
	writer.Flush()
	file.Close()
}

func RestoreCache() {
	CacheMutex.Lock()
	defer CacheMutex.Unlock()

	files, err := filepath.Glob(CacheFolderPath + "*")
	CheckError("err restoring cache. Cannot fetch file names", err)

	for _, fileName := range files {
		fileName = strings.TrimPrefix(fileName, "cache/")

		if fileName == ".DS_Store" {
			continue
		}
		MemoryCache[fileName] = ReadFromDisk(fileName)
	}

	for key := range MemoryCache {
		if isExpired(key) {
			DeleteCacheEntry(key)
		}
	}
}

func Encrypt(input string) string {
	var bytes []byte = []byte(input)
	var code [20]byte = sha1.Sum(bytes)
	var s string = string(code[:])
	return strconv.QuoteToASCII(s)
}

func ReadFromDisk(hash string) CacheEntry {
	data, err := ioutil.ReadFile(CacheFolderPath + hash)
	CheckError("read error from disk", err)

	var cacheEntry CacheEntry
	err = json.Unmarshal(data, &cacheEntry)
	CheckError("json unmarshal err", err)
	return cacheEntry
}

func DeleteFromDisk(fileHash string) {
	err := os.Remove(CacheFolderPath + fileHash)
	CheckError("remove file error", err)
}

func DeleteCacheEntry(hashkey string) {
	delete(MemoryCache, hashkey)
	DeleteFromDisk(hashkey)
}

func DeleteEntryElephant(hashkey string) {
	delete(MemoryCache, hashkey)
}


func Evict() {
	EvictExpired()

	if len(MemoryCache) >= options.CacheSize {
		var KeyToEvict string
		if options.EvictPolicy == "LRU" {
			KeyToEvict = EvictLRU()
		} else if options.EvictPolicy == "LFU" {
			KeyToEvict = EvictLFU()
		} else {
			KeyToEvict = EvictLRU()
			DeleteEntryElephant(KeyToEvict)
			return
		}
		DeleteCacheEntry(KeyToEvict)
	}
}

func EvictLRU() string {
	oldestTime := time.Now()
	oldestKey := ""
	for key, cacheEntry := range MemoryCache {
		if cacheEntry.LastAccess.Before(oldestTime) {
			oldestKey = key
			oldestTime = cacheEntry.LastAccess
		}
	}
	return oldestKey
}

func EvictLFU() string {
	var mostFrequentNumber uint64
	bestKey := ""
	for key, cacheEntry := range MemoryCache {
		if cacheEntry.UseFreq > mostFrequentNumber {
			bestKey = key
			mostFrequentNumber = cacheEntry.UseFreq
		}
	}
	return bestKey
}

func EvictExpired() {
	CacheMutex.Lock()
	for key := range MemoryCache {
		if isExpired(key) {
			DeleteCacheEntry(key)
		}
	}
	CacheMutex.Unlock()
}

func isExpired(hash string) bool {
	cache, _ := MemoryCache[hash]
	elapsed := time.Since(cache.CreateTime)
	if elapsed > options.ExpirationTime {
		return true
	} else {
		return false
	}
}

// ===========================================================
// ===========================================================
//					Helper
// ===========================================================
// ===========================================================

func CheckError(msg string, err error) {
	if err != nil {
		fmt.Println("***********************************")
		fmt.Println("***********************************")
		fmt.Println(msg)
		fmt.Println("***********************************")
		log.Fatal(err)
		fmt.Println("***********************************")
		fmt.Println("***********************************")
	}
}

func DebugPrint(title string, msg string) {
	fmt.Println("============ " + title + " ===============")
	fmt.Println(msg)
	fmt.Println("---------------------------------------")
}

func NewRequest(w http.ResponseWriter, r *http.Request) *http.Response {
	var resp *http.Response
	var newRequest *http.Request
	client := &http.Client{}

	newRequest, err := http.NewRequest(r.Method, r.RequestURI, r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		fmt.Println("cannot fetch response in new request")
		return nil
	}

	resp, err = client.Do(newRequest)

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		fmt.Println("cannot fetch response in new request")
		return nil
	}

	return resp
}
