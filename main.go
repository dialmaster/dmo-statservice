package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

var c conf
var mutex = &sync.Mutex{}

type BlockInformation struct {
	Time   int
	Height int
	TxId   string
	Hash   string
	Addr   string
	Coins  float64
}

// Memory cache of blocks, with height as the key...
var blockMap = make(map[int]BlockInformation)

// Get mining info for a range of blocks

/*
What this program NEEDS to do:
1 - When it starts, get the current block height
2 - connect to the DB and see what the highest block it HAS is

Init:
3 - Start at the current block height -  (110000 -highest block in db)
4 - go up to the current block
5 - get the data for all those blocks and insert them

Once init is done:
6 - Fire up an http server and expose a service to get information for a given receiving address
  This RPC needs to take 2 args:
  {
	  "receivingAddresses": "addr1,addr2,addr3...",
	  "timeZone": "UTC-6"
  }

  That service should scan the last 110000 blocks are return stats for mined coins for the receiving addresses given:
  daily stats for the last 21 days
  hourly stats for today

  It should return a JSON structure like:
{
	"dayStats" : [  // returns days from 21 days ago to now, in order... the last element in the array is today.
			{
			"coins": 100.33,
			"totalBlocksForDate": 4500
			},
			{
			"coins": 200.33,
			"totalBlocksForDate": 4600
			}

	],

	"hourStats": [

	]
}

7 - once that is done, then it should run once every 5 minutes and do that again



*/
var db *sql.DB
var dbErr error

var currentHeight int
var currentDBHeight int
var lowestDBHeight int
var blockHistoryDepth int

func main() {
	c.getConf()
	currentDBHeight = 0
	lowestDBHeight = 5000000000

	blockHistoryDepth = 100000

	db, dbErr = sql.Open("mysql", c.ServiceDBUser+":"+c.ServiceDBPass+"@tcp("+c.ServiceDBIP+":"+c.ServiceDBPort+")/"+c.ServiceDBName)
	// if there is an error opening the connection, handle it
	if dbErr != nil {
		panic(dbErr.Error())
	}
	defer db.Close()
	fmt.Printf("Connected to DB: %s\n", c.ServiceDBName)

	currentHeight = getCurrentHeight()
	fmt.Printf("Current block height from node: %d\n", currentHeight)

	fmt.Printf("Initializing...\n")
	fmt.Printf("Loading new blocks from DB...\n")
	updateStats()
	fmt.Printf("Lowest DB height is %d", lowestDBHeight)
	fmt.Printf("Highest DB height is %d", currentDBHeight)
	fmt.Printf("Done loading new blocks from DB!\n")
	fmt.Printf("Caching DB to memory...\n")
	loadDBStatsToMemory()
	fmt.Printf("DB cache to memory complete!\n")

	myTime := time.Now()
	zone_name, offset := myTime.Zone()

	// Prints zone name
	fmt.Printf("The zone name is: %s\n", zone_name)
	fmt.Printf("The zone offset is: %d\n", offset)

	// These are the helper funcs we will need to return stats....
	fmt.Printf("Epoch of beginning of day today in EST %d\n", getTZDayStart(offset, 0))

	fmt.Printf("This hour in EST is %d\n", getCurrentTZHour(offset))

	fmt.Printf("Epoch of beginning of this hour in EST %d\n", getTZHourStart(offset, 0))

	// Grab new block info from the node every minute
	go func() {
		fmt.Printf("Service is RUNNING on port %s\n", c.ServicePort)
		for {
			time.Sleep(60 * time.Second)
			updateStats()
		}
	}()
	handleRequests()

}

// Load ALL DB Stats into memory!
func loadDBStatsToMemory() {
	type DBResult struct {
		Height_id  int     `json:"height_id"`
		Blockhash  string  `json:"blockhash"`
		Epoch      int     `json:"epoch"`
		Coins      float64 `json:"coins"`
		Miningaddr string  `json:"miningaddr"`
	}

	// Execute the query
	results, err := db.Query("select height_id, blockhash, epoch, coins, miningaddr from stats")
	if err != nil {
		panic(err.Error()) // proper error handling instead of panic in your app
	}

	for results.Next() {
		var dbResult DBResult
		// for each row, scan the result into our tag composite object
		err = results.Scan(&dbResult.Height_id, &dbResult.Blockhash, &dbResult.Epoch, &dbResult.Coins, &dbResult.Miningaddr)
		if err != nil {
			panic(err.Error()) // proper error handling instead of panic in your app
		}
		var myStatResult BlockInformation
		myStatResult.Addr = dbResult.Miningaddr
		myStatResult.Coins = dbResult.Coins
		myStatResult.Height = dbResult.Height_id
		myStatResult.Hash = dbResult.Blockhash
		myStatResult.Time = dbResult.Epoch
		mutex.Lock()
		blockMap[dbResult.Height_id] = myStatResult
		mutex.Unlock()
	}

}

// Take a TZ and a day offset from today (0 for today, -1 for yesterday)
// Return the unix epoch when that day started
func getTZDayStart(tzOffset int, dayOffset int) int64 {
	loc := time.FixedZone("MonitorZone", tzOffset)
	myTime := time.Now().In(loc)
	myTime = myTime.AddDate(0, 0, dayOffset)
	return time.Date(myTime.Year(), myTime.Month(), myTime.Day(), 0, 0, 0, 0, loc).Unix()
}

// Take a TZ and an hour offset from this hour, return the unix epoch when that hour started
// (0 for beginning of this hour, -1 for beginning of the last hour)
// Take a TZ and a day offset from today (0 for today, -1 for yesterday)
// Return the unix epoch when that day started
func getTZHourStart(tzOffset int, hourOffset int) int64 {
	loc := time.FixedZone("MonitorZone", tzOffset)
	myTime := time.Now().In(loc)
	myTime = myTime.Add(time.Hour * time.Duration(hourOffset))
	return time.Date(myTime.Year(), myTime.Month(), myTime.Day(), myTime.Hour(), 0, 0, 0, loc).Unix()
}

// Get the current hour of the day in the timezone passed
func getCurrentTZHour(tzOffset int) int {
	loc := time.FixedZone("MonitorZone", tzOffset)
	myTime := time.Now().In(loc)
	return myTime.Hour()
}

func handleRequests() {
	http.HandleFunc("/getminingstats", getAddrMiningStatsRPC)

	log.Fatal(http.ListenAndServe(":"+c.ServicePort, nil))

}

// Load blocks from node up to current block. Do not expose RPC server until this is done. Display some output to user
// TODO: This should ALSO load stats into memory... We wil get statistics from memory later
func updateStats() {

	currentHeight = getCurrentHeight()
	fmt.Printf("Current block height from node: %d\n", currentHeight)

	type DBResult struct {
		height_id int `json:"height_id"`
	}

	results, err := db.Query("select height_id from stats order by height_id desc limit 0,1")
	if err != nil {
		panic(err.Error()) // proper error handling instead of panic in your app
	}

	for results.Next() {
		var dbResult DBResult
		// for each row, scan the result into our tag composite object
		err = results.Scan(&dbResult.height_id)
		if err != nil {
			panic(err.Error()) // proper error handling instead of panic in your app
		}
		// and then print out the tag's Name attribute
		log.Printf("Got height_id: %d", dbResult.height_id)
		currentDBHeight = dbResult.height_id
	}

	results, err = db.Query("select height_id from stats order by height_id asc limit 0,1")
	if err != nil {
		panic(err.Error()) // proper error handling instead of panic in your app
	}

	for results.Next() {
		var dbResult DBResult
		// for each row, scan the result into our tag composite object
		err = results.Scan(&dbResult.height_id)
		if err != nil {
			panic(err.Error()) // proper error handling instead of panic in your app
		}
		// and then print out the tag's Name attribute
		log.Printf("Got height_id: %d", dbResult.height_id)
		lowestDBHeight = dbResult.height_id
	}

	var startHeight = currentDBHeight

	if startHeight < (currentHeight - blockHistoryDepth) {
		startHeight = currentHeight - blockHistoryDepth
	} else {
		startHeight = currentDBHeight + 1
	}

	blockIdToGet := startHeight
	fmt.Printf("Grabbing %d new blocks from node...\n", currentHeight-blockIdToGet)

	var myBlockInfo BlockInformation
	for blockIdToGet < currentHeight {
		myBlockInfo = getFullBlockInfoForHeight(blockIdToGet)
		mutex.Lock()
		blockMap[myBlockInfo.Height] = myBlockInfo // Add to memory cache
		mutex.Unlock()
		insert, err := db.Query("INSERT INTO stats (height_id, blockhash, epoch, coins, miningaddr) VALUES ( " + strconv.Itoa(blockIdToGet) + ", '" + myBlockInfo.Hash + "', " + strconv.Itoa(myBlockInfo.Time) + ", " + strconv.FormatFloat(myBlockInfo.Coins, 'E', -1, 64) + ",'" + myBlockInfo.Addr + "')")

		// if there is an error inserting, handle it
		if err != nil {
			panic(err.Error())
		}
		// be careful deferring Queries if you are using transactions
		insert.Close()

		blockIdToGet += 1
		if (blockIdToGet % 500) == 0 {
			fmt.Printf("Grabbed up to block id %d\n", blockIdToGet)
		}
	}
	fmt.Printf("DB update from Node is complete!\n")

}

type mineRpc struct {
	Addresses string
	TZOffset  int
	NumDays   int
}

func contains(s []string, str string) bool {
	for _, v := range s {
		if v == str {
			return true
		}
	}

	return false
}

/* Accept json request like:
{
    "Addresses": "dy1qpfr5yhdkgs6jyuk945y23pskdxmy9ajefczsvm,kljdsalkjsadlksajd",
	"TZOffset": -28800
}
*/
// TODO: Do not allow more than 10 receiving addresses
func getAddrMiningStatsRPC(rw http.ResponseWriter, req *http.Request) {
	decoder := json.NewDecoder(req.Body)
	var jsonBody mineRpc
	err := decoder.Decode(&jsonBody)
	if err != nil {
		panic(err)
	}

	type HourStat struct {
		Hour       int
		Coins      float64
		ChainCoins float64
		WinPercent float64
	}

	var hourStats []HourStat
	fmt.Printf("Getting stats for addresse(s) %s\n", jsonBody.Addresses)
	start := time.Now()

	// Fill up HourStats array with all hours for today...
	hoursToday := getCurrentTZHour(jsonBody.TZOffset)
	for i := 0; i <= hoursToday; i++ {
		curHour := i - hoursToday
		startEpoch := getTZHourStart(jsonBody.TZOffset, curHour)
		endEpoch := startEpoch + 3600
		var thisHour HourStat

		thisHour.Coins = getCoinsInEpochRange(startEpoch, endEpoch, jsonBody.Addresses)
		thisHour.ChainCoins = getCoinsInEpochRange(startEpoch, endEpoch, "")
		thisHour.WinPercent = thisHour.Coins * 100.0 / thisHour.ChainCoins
		thisHour.Hour = i
		hourStats = append(hourStats, thisHour)

	}

	type DayStat struct {
		Day        string
		Coins      float64
		ChainCoins float64
		WinPercent float64
	}

	var dayStats []DayStat

	//	Fill up days...
	numDays := jsonBody.NumDays
	if numDays < 2 {
		numDays = 2
	}
	if numDays > 21 {
		numDays = 21
	}
	for i := 0; i <= numDays; i++ {
		curDay := i - numDays
		startEpoch := getTZDayStart(jsonBody.TZOffset, curDay)
		endEpoch := startEpoch + 86400
		var thisDay DayStat

		thisDay.Coins = getCoinsInEpochRange(startEpoch, endEpoch, jsonBody.Addresses)
		thisDay.ChainCoins = getCoinsInEpochRange(startEpoch, endEpoch, "")
		thisDay.WinPercent = thisDay.Coins * 100.0 / thisDay.ChainCoins
		formattedTime := time.Unix(startEpoch, 0).Format("2006-01-02")
		thisDay.Day = formattedTime
		dayStats = append(dayStats, thisDay)
	}

	type ResponseStats struct {
		HourlyStats         []HourStat
		DailyStats          []DayStat
		ProjectedCoinsToday float64
	}

	secondsSoFarToday := float64(time.Now().Unix()-getTZDayStart(jsonBody.TZOffset, 0)) + 1.0

	var thisResponse ResponseStats
	thisResponse.ProjectedCoinsToday = dayStats[len(dayStats)-1].Coins * (86400.0 / secondsSoFarToday)
	thisResponse.HourlyStats = hourStats
	thisResponse.DailyStats = dayStats
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(http.StatusCreated)

	elapsed := time.Since(start)
	log.Printf("RPC Execution time: %s", elapsed)

	json.NewEncoder(rw).Encode(thisResponse)
}

type statsForRange struct {
	MyCoins  float64 // Coins for addresses in range
	AllCoins float64 // ALL coins mined in range
	MyPerc   float64 // my percent of all coins for range
}

// Get lowest and highest block for epoch range
func findBlocksForEpochRange(startEpoch int64, endEpoch int64) (int, int) {
	//fmt.Printf("Looking for lowest/highest between %d and %d\n", startEpoch, endEpoch)
	lowest := lowestDBHeight
	highest := currentDBHeight

	found := 0
	for found == 0 {
		if blockMap[lowest].Time > int(startEpoch) || lowest > currentDBHeight {
			lowest -= 512
			found = 1
		}

		lowest += 256

	}

	found = 0
	for found == 0 {
		if blockMap[highest].Time < int(endEpoch) || highest < lowestDBHeight {
			highest += 512
			found = 1
		}
		highest -= 256
	}

	if lowest < lowestDBHeight {
		lowest = lowestDBHeight
	}

	if highest > currentDBHeight {
		highest = currentDBHeight
	}
	//fmt.Printf("Found lowest %d at %d\n", lowest, blockMap[lowest].Time)
	//fmt.Printf("Found highest %d at %d\n", highest, blockMap[highest].Time)

	return lowest, highest
}

// Get the number of coins in a given epoch range. If addresses is passed, limit count to coins
// for those addresses. If not then just get all mined coins in range count...
func getCoinsInEpochRange(startEpoch int64, endEpoch int64, addresses string) float64 {
	addrsToCheck := strings.Split(addresses, ",")
	numCoins := 0.0

	lowest, highest := findBlocksForEpochRange(startEpoch, endEpoch)

	//TODO: Do not loop over entire blockmap... instead only check heights that COULD even contain the epochs
	for i := lowest; i < highest; i++ {
		//for _, block := range blockMap {
		if block, ok := blockMap[i]; ok {
			if len(addrsToCheck) > 0 && len(addrsToCheck[0]) > 0 {
				if contains(addrsToCheck, block.Addr) {
					if startEpoch < int64(block.Time) && int64(block.Time) < endEpoch {
						numCoins += block.Coins
					}
				}
			} else {
				if startEpoch < int64(block.Time) && int64(block.Time) < endEpoch {
					numCoins += block.Coins
				}
			}
		}
	}

	return numCoins
}

func epochToString(epoch int) string {
	unixTimeUTC := time.Unix(int64(epoch), 0) //gives unix time stamp in utc
	unitTimeInRFC3339 := unixTimeUTC.Format(time.RFC3339)

	return unitTimeInRFC3339
}

func getFullBlockInfoForHeight(height int) BlockInformation {
	var myBlockInfo BlockInformation
	myBlockInfo.Height = height
	myBlockInfo = getBlockHash(myBlockInfo)
	myBlockInfo = getBlock(myBlockInfo)
	myBlockInfo = getTransInfo(myBlockInfo)
	return myBlockInfo
}

func getCurrentHeight() int {
	type blockHeightResult struct {
		ID     string `json:"id"`
		Result int    `json:"result"`
		error  string `json:"error"`
	}

	client := &http.Client{}
	reqUrl := url.URL{
		Scheme: "http",
		Host:   c.NodeIP + ":" + c.NodePort,
		Path:   "",
	}

	var data = bytes.NewBufferString(`{"jsonrpc":"1.0","id":"curltest","method":"getblockcount", "params": { }}`)
	req, err := http.NewRequest("POST", reqUrl.String(), data)
	req.SetBasicAuth(c.NodeUser, c.NodePass)
	resp, err := client.Do(req)
	if err != nil {
		log.Fatal(err)
		return 0
	}
	bodyText, err := io.ReadAll(resp.Body)

	var myBlockHeight blockHeightResult

	if err := json.Unmarshal(bodyText, &myBlockHeight); err != nil {
		return 0
	}
	return myBlockHeight.Result
}

// Step one, get the block hash for the block number
func getBlockHash(blockInfo BlockInformation) BlockInformation {

	type blockHashResult struct {
		ID     string `json:"id"`
		Result string `json:"result"`
		error  string `json:"error"`
	}

	client := &http.Client{}
	reqUrl := url.URL{
		Scheme: "http",
		Host:   c.NodeIP + ":" + c.NodePort,
		Path:   "",
	}

	var data = bytes.NewBufferString(`{"jsonrpc":"1.0","id":"curltest","method":"getblockhash", "params": { "height": ` + strconv.Itoa(blockInfo.Height) + `}}`)
	req, err := http.NewRequest("POST", reqUrl.String(), data)
	req.SetBasicAuth(c.NodeUser, c.NodePass)
	resp, err := client.Do(req)
	if err != nil {
		log.Fatal(err)
		//return "Unable to make request to getBlockHash: " + err.Error()
		return blockInfo
	}
	bodyText, err := io.ReadAll(resp.Body)

	var myBlockHash blockHashResult

	if err := json.Unmarshal(bodyText, &myBlockHash); err != nil {
		//return "Unable to decode json from getblockhash request: " + err.Error()
		return blockInfo
	}
	blockInfo.Hash = myBlockHash.Result

	return blockInfo
}

// Step two, get the block for the hash... returns a txid IF IT WAS A MINED BLOCK
func getBlock(blockInfo BlockInformation) BlockInformation {
	type blockResult struct {
		Result struct {
			Hash              string   `json:"hash"`
			Confirmations     int      `json:"confirmations"`
			Height            int      `json:"height"`
			Version           int      `json:"version"`
			VersionHex        string   `json:"versionHex"`
			Merkleroot        string   `json:"merkleroot"`
			Time              int      `json:"time"`
			Mediantime        int      `json:"mediantime"`
			Nonce             int      `json:"nonce"`
			Bits              string   `json:"bits"`
			Difficulty        float64  `json:"difficulty"`
			Chainwork         string   `json:"chainwork"`
			NTx               int      `json:"nTx"`
			Previousblockhash string   `json:"previousblockhash"`
			Nextblockhash     string   `json:"nextblockhash"`
			Strippedsize      int      `json:"strippedsize"`
			Size              int      `json:"size"`
			Weight            int      `json:"weight"`
			Tx                []string `json:"tx"`
		} `json:"result"`
		Error interface{} `json:"error"`
		ID    string      `json:"id"`
	}

	client := &http.Client{}
	reqUrl := url.URL{
		Scheme: "http",
		Host:   c.NodeIP + ":" + c.NodePort,
		Path:   "",
	}

	var data = bytes.NewBufferString(`{"jsonrpc":"1.0","id":"curltest","method":"getblock", "params": { "blockhash": "` + blockInfo.Hash + `"}}`)
	req, err := http.NewRequest("POST", reqUrl.String(), data)
	req.SetBasicAuth(c.NodeUser, c.NodePass)
	resp, err := client.Do(req)
	if err != nil {
		log.Fatal(err)
		return blockInfo
	}

	bodyText, err := io.ReadAll(resp.Body)

	var myBlock blockResult

	if err := json.Unmarshal(bodyText, &myBlock); err != nil {
		return blockInfo
	}

	blockInfo.Time = myBlock.Result.Time
	blockInfo.TxId = myBlock.Result.Tx[0]
	return blockInfo
}

type MinedTxInfo struct {
	miningAddr string
	coins      float64
}

// Step three, get the information I care about
func getTransInfo(blockInfo BlockInformation) BlockInformation {

	var myInfo MinedTxInfo

	myInfo.miningAddr = "professorminingaddr"
	myInfo.coins = 1.001

	type TransResponse struct {
		Result struct {
			InActiveChain bool   `json:"in_active_chain"`
			Txid          string `json:"txid"`
			Hash          string `json:"hash"`
			Version       int    `json:"version"`
			Size          int    `json:"size"`
			Vsize         int    `json:"vsize"`
			Weight        int    `json:"weight"`
			Locktime      int    `json:"locktime"`
			Vin           []struct {
				Coinbase    string   `json:"coinbase"`
				Txinwitness []string `json:"txinwitness"`
				Sequence    int64    `json:"sequence"`
			} `json:"vin"`
			Vout []struct {
				Value        float64 `json:"value"`
				N            int     `json:"n"`
				ScriptPubKey struct {
					Asm     string `json:"asm"`
					Hex     string `json:"hex"`
					Address string `json:"address"`
					Type    string `json:"type"`
				} `json:"scriptPubKey,omitempty"`
			} `json:"vout"`
			Hex           string `json:"hex"`
			Blockhash     string `json:"blockhash"`
			Confirmations int    `json:"confirmations"`
			Time          int    `json:"time"`
			Blocktime     int    `json:"blocktime"`
		} `json:"result"`
		Error interface{} `json:"error"`
		ID    string      `json:"id"`
	}

	client := &http.Client{}
	reqUrl := url.URL{
		Scheme: "http",
		Host:   c.NodeIP + ":" + c.NodePort,
		Path:   "",
	}

	var data = bytes.NewBufferString(`{"jsonrpc":"1.0","id":"curltest","method":"getrawtransaction", "params": { "blockhash": "` + blockInfo.Hash + `", "txid": "` + blockInfo.TxId + `", "verbose": true}}`)
	req, err := http.NewRequest("POST", reqUrl.String(), data)
	req.SetBasicAuth(c.NodeUser, c.NodePass)
	resp, err := client.Do(req)
	if err != nil {
		log.Fatal(err)
		return blockInfo
	}
	bodyText, err := io.ReadAll(resp.Body)
	//fmt.Printf("Raw trans info: %s\n", bodyText)

	var myTrans TransResponse

	if err := json.Unmarshal(bodyText, &myTrans); err != nil {
		return blockInfo
	}

	if myTrans.Result.Vout[0].Value > 2.0 {
		fmt.Printf("Coinbase value %s, coins %.2f\n", myTrans.Result.Vin[0].Coinbase, myTrans.Result.Vout[0].Value)
	}

	blockInfo.Addr = myTrans.Result.Vout[0].ScriptPubKey.Address
	// TODO: CHECK THIS, will non-mine transactions just have no string here?
	if len(myTrans.Result.Vin[0].Coinbase) > 0 {
		blockInfo.Coins = myTrans.Result.Vout[0].Value
	} else {
		blockInfo.Coins = 0.0 // If it wasn't a MINED transaction, don't count the coins!
	}

	return blockInfo

}
