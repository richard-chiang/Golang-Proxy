package main

import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
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
		ExpirationTime: time.Duration(30) * time.Second}

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
	//fmt.Println("Request", r.URL)
	if r.Method == "GET" {
		// Cache <- Response
		entry, existInCache := GetByURL(r.RequestURI)
		if existInCache {
			//fmt.Println("Found ", r.RequestURI)
		}
		if !existInCache {
			hashArr := strings.Split(r.RequestURI, "/")
			if len(hashArr) > 3 {
				hash := hashArr[3]
				entry, existInCache = GetByHash(hash)
			}
			if existInCache {
				//fmt.Println("Found ", r.RequestURI)
			}
		}

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
				ForwardResponseToFireFox(w, resp)
				return
			}
			//CacheControl := resp.Header.Get("Cache-Control")
			//if CacheControl == "no-cache" {
			//	ForwardResponseToFireFox(w, resp)
			//	return
			//}

			data, err := ioutil.ReadAll(resp.Body)
			if err != nil {
				fmt.Println("Something wrong while reading body")
			}
			newEntry := NewCacheEntry(data)
			newEntry.RawData = data
			newEntry.Header = http.Header{}
			for name, values := range resp.Header {
				for _, v := range values {
					newEntry.Header.Add(name, v)
				}
			}

			if strings.Contains(http.DetectContentType(data), "text/html") {
				PrintLine("129")
				PrintLine("131")
				urlsToReplace := ParseHTML(data) // grab resources
				PrintLine("133")
				newHTML := WriteHTML(data, urlsToReplace) // modify html
				PrintLine("135")
				newEntry.RawData = []byte(newHTML)
				PrintLine("137")
			}
			entry = newEntry
			AddCacheEntry(r.RequestURI, newEntry) // save original html
			resp.Body.Close()
		}

		for name, values := range entry.Header {
			for _, v := range values {
				w.Header().Add(name, v)
			}
		}
		//fmt.Printf("Writing response %d bytes \n",len(entry.RawData))
		_, err := io.Copy(w, bytes.NewReader(entry.RawData))

		CheckError("io copy", err)

	} else {
		resp := NewRequest(w, r)
		ForwardResponseToFireFox(w, resp)
	}
}

func ForwardResponseToFireFox(w http.ResponseWriter, resp *http.Response) {
	// forward response to firefox
	PrintLine("158")

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

func WriteHTML(data []byte, urlsToReplace []string) string {
	htmlString := string(data)

	for _, url := range urlsToReplace {
		htmlString = strings.Replace(htmlString, url, Encrypt(url), -1)
	}
	return htmlString
}

// temperory function: just for testing if a link can be found
func printList(data []string) {
	for _, v := range data {
		fmt.Println(v)
	}
}

// example
// tagData = "img"
// keyword = "src"

func ParseHTML(resp []byte) []string {
	PrintLine("267")
	const LINK_TAG = "link"
	const IMG_TAG = "img"
	const SCRIPT_TAG = "script"
	PrintLine("271")
	cursor := html.NewTokenizer(bytes.NewReader(resp))
	var urlsToReplace []string

	for {
		PrintLine("274")
		token := cursor.Next()
		switch token {
		case html.ErrorToken:
			PrintLine("278")
			return urlsToReplace
		case html.StartTagToken:
			//fmt.Println("NOT ERROR")
			fetchedToken := cursor.Token()
			PrintLine("283")
			//fmt.Println(fetchedToken.Data, fetchedToken.Attr)
			switch fetchedToken.Data {
			case LINK_TAG:
				for _, a := range fetchedToken.Attr {
					if a.Key == "href" && strings.HasPrefix(a.Val, "http") {
						urlsToReplace = append(urlsToReplace, a.Val)
						PrintLine("287")
						RequestResource(a)
						PrintLine("289")
					}
				}
			case IMG_TAG:
				for _, a := range fetchedToken.Attr {
					if a.Key == "src" && strings.HasPrefix(a.Val, "http") {
						urlsToReplace = append(urlsToReplace, a.Val)
						PrintLine("293")
						RequestResource(a)
						PrintLine("297")
					}
				}
			case SCRIPT_TAG:
				for _, a := range fetchedToken.Attr {

					if a.Key == "src" && strings.HasPrefix(a.Val, "http") {
						urlsToReplace = append(urlsToReplace, a.Val)
						PrintLine("301")
						RequestResource(a)
						PrintLine("307")
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
			MemoryCache[fileName] = ReadFromDisk(fileName)
			for len(MemoryCache) >= options.CacheSize {
				Evict()
			}
			return MemoryCache[fileName], true
		}
	}

	return CacheEntry{}, false
}

func GetFromDiskUrl(url string) (CacheEntry, bool) {
	hashkey := Encrypt(url)
	return GetFromDiskHash(hashkey)
}

// hostname: http://example.com
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
	PrintLine("376")
	resp, err := http.Get(a.Val)
	if err != nil {
		time.Sleep(time.Second)
		resp, err = http.Get(a.Val)
	}
	CheckError("request resource: get request", err)
	PrintLine("383")
	bytes, err := ioutil.ReadAll(resp.Body)
	PrintLine("385")
	CheckError("request resource: readall", err)
	entry := NewCacheEntry(bytes)
	PrintLine("388")
	entry.RawData = bytes
	AddCacheEntry(a.Val, entry)
	PrintLine("391")
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
	PrintLine("413")
	CacheMutex.Lock()
	PrintLine("415")
	for len(MemoryCache) >= options.CacheSize {
		Evict()
	}
	PrintLine("419")
	fileName := Encrypt(URL)
	PrintLine("421")
	MemoryCache[fileName] = entry
	PrintLine("423")
	WriteToDisk(fileName, entry)
	PrintLine("425")
	CacheMutex.Unlock()
	PrintLine("427")
}

func WriteToDisk(fileHash string, entry CacheEntry) {
	bytes, err := json.Marshal(entry)
	CheckError("json marshal error", err)
	filePath := CacheFolderPath + fileHash

	_, err = os.Stat(filePath)
	if err != nil { // file does not exist, do create
		file, err := os.Create(filePath)
		CheckError("Create File Error", err)
		defer file.Close()

		writer := bufio.NewWriter(file)
		writer.Write(bytes)
		writer.Flush()
	} else { // file exist, do write
		file, err := os.OpenFile(filePath, os.O_WRONLY, 0666)
		CheckError("open existing file error", err)
		defer file.Close()

		bufferedWriter := bufio.NewWriter(file)
		bytesWritten, err := bufferedWriter.Write(bytes)
		if err != nil || bytesWritten != len(bytes) {
			fmt.Println(err.Error())
			fmt.Println("maybe not enough bytes written on file")
			return
		}

		bufferedWriter.Flush()
		bufferedWriter.Reset(bufferedWriter)
		os.Truncate(filePath, int64(bytesWritten))
	}
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
	h := sha1.New()
	h.Write([]byte(input))
	sha := base64.URLEncoding.EncodeToString(h.Sum(nil))
	return sha
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
	PrintLine("518")
	if len(MemoryCache) >= options.CacheSize {
		var KeyToEvict string
		if options.EvictPolicy == "LRU" {
			KeyToEvict = EvictLRU()
		} else if options.EvictPolicy == "LFU" {
			KeyToEvict = EvictLFU()
		} else {
			KeyToEvict = EvictLRU()
			DeleteEntryElephant(KeyToEvict)
			PrintLine("528")
			return
		}
		DeleteCacheEntry(KeyToEvict)
	}
	PrintLine("533")
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
	PrintLine("549")
	var mostFrequentNumber uint64
	bestKey := ""
	PrintLine("552")
	for key, cacheEntry := range MemoryCache {
		if cacheEntry.UseFreq > mostFrequentNumber {
			bestKey = key
			mostFrequentNumber = cacheEntry.UseFreq
		}
	}
	PrintLine("559")
	return bestKey
}

func EvictExpired() {
	for key := range MemoryCache {
		if isExpired(key) {
			DeleteCacheEntry(key)
		}
	}
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

func PrintLine(line string) {
	// fmt.Println("On Line " + line)
}

func PrintMemoryCache() {
	for key, _ := range MemoryCache {
		fmt.Println("========= Cache ============")
		fmt.Println("key: " + key)
		fmt.Println("=========================")
	}
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
