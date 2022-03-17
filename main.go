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

	"github.com/gin-gonic/gin"

	_ "github.com/go-sql-driver/mysql"
)

var c conf
var mutex = &sync.Mutex{}

type blockInformation struct {
	Time   int
	Height int
	TxID   string
	Hash   string
	Addr   string
	Coins  float64
}

var blockMap = make(map[int]blockInformation)

type pogoInfoForAddr struct {
	lastUpdate   int64
	coinsAtTimes map[int64]float64
}

// Key is a receiving addr
var pogoCoins = make(map[string]pogoInfoForAddr)

var db *sql.DB
var dbErr error

var currentHeight int
var currentDBHeight int
var lowestDBHeight int
var blockHistoryDepth int
var globalNetHash float64

func main() {
	gin.SetMode(gin.ReleaseMode)
	router := gin.Default()

	c.getConf()
	currentDBHeight = 0
	lowestDBHeight = 5000000000
	globalNetHash = 0.0

	blockHistoryDepth = 100000

	db, dbErr = sql.Open("mysql", c.ServiceDBUser+":"+c.ServiceDBPass+"@tcp("+c.ServiceDBIP+":"+c.ServiceDBPort+")/"+c.ServiceDBName)
	// Truly a fatal error.
	if dbErr != nil {
		panic(dbErr.Error())
	}
	defer db.Close()
	fmt.Printf("Connected to DB: %s\n", c.ServiceDBName)

	getDBHeight()

	currentHeight, err := getCurrentHeight()
	if err != nil {
		fmt.Printf("Unable to connect to node, using DB cache only, height %d: Error %s", currentDBHeight, err)
		currentHeight = currentDBHeight
	}
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

	// Grab new block info from the node every minute
	go func() {
		fmt.Printf("Service is RUNNING on port %s\n", c.ServicePort)
		for {
			time.Sleep(60 * time.Second)
			updateStats()
		}
	}()

	router.GET("/getminingstats", getAddrMiningStatsRPC)
	err = router.Run(":" + c.ServicePort)
	if err != nil {
		log.Fatalf("Unable to start router: %s", err)
	}
}

func getDBHeight() {
	type DBResult struct {
		HeightID int `json:"height_id"`
	}

	results, err := db.Query("select height_id from stats order by height_id desc limit 0,1")
	// Fatal, we need our DB
	if err != nil {
		panic(err.Error())
	}

	for results.Next() {
		var dbResult DBResult
		err = results.Scan(&dbResult.HeightID)
		if err != nil {
			panic(err.Error())
		}
		currentDBHeight = dbResult.HeightID
	}

}

func loadDBStatsToMemory() {
	type DBResult struct {
		HeightID   int     `json:"height_id"`
		Blockhash  string  `json:"blockhash"`
		Epoch      int     `json:"epoch"`
		Coins      float64 `json:"coins"`
		Miningaddr string  `json:"miningaddr"`
	}

	results, err := db.Query("select height_id, blockhash, epoch, coins, miningaddr from stats")
	if err != nil {
		panic(err.Error())
	}

	for results.Next() {
		var dbResult DBResult
		err = results.Scan(&dbResult.HeightID, &dbResult.Blockhash, &dbResult.Epoch, &dbResult.Coins, &dbResult.Miningaddr)
		if err != nil {
			panic(err.Error())
		}
		var myStatResult blockInformation
		myStatResult.Addr = dbResult.Miningaddr
		myStatResult.Coins = dbResult.Coins
		myStatResult.Height = dbResult.HeightID
		myStatResult.Hash = dbResult.Blockhash
		myStatResult.Time = dbResult.Epoch
		mutex.Lock()
		blockMap[dbResult.HeightID] = myStatResult
		mutex.Unlock()
	}

}

// ALL TIMES IN UTC

func getDayStart(dayOffset int) int64 {
	myTime := time.Now().UTC()
	myTime = myTime.AddDate(0, 0, dayOffset)
	return time.Date(myTime.Year(), myTime.Month(), myTime.Day(), 0, 0, 0, 0, time.UTC).Unix()
}

func getHourStart(hourOffset int) int64 {
	myTime := time.Now().UTC()
	myTime = myTime.Add(time.Hour * time.Duration(hourOffset))
	return time.Date(myTime.Year(), myTime.Month(), myTime.Day(), myTime.Hour(), 0, 0, 0, time.UTC).Unix()
}

func getCurrentHour() int {
	myTime := time.Now().UTC()
	return myTime.Hour()
}

// Make RPC to POGO and update pogoCoins
func getPogoInfoForAddr(addr string) {

	startEpoch := getHourStart(0)
	if curVal, ok := pogoCoins[addr]; ok {
		if curVal.lastUpdate > startEpoch {
			fmt.Printf("No need to get new info from POGO!\n")
			return
		}
	}

	fmt.Printf("Getting new info from POGO!\n")
	var newPogoInfoForAddr pogoInfoForAddr
	var newCoinsAtTimes = make(map[int64]float64)
	newPogoInfoForAddr.coinsAtTimes = newCoinsAtTimes
	newPogoInfoForAddr.lastUpdate = time.Now().Unix()
	pogoCoins[addr] = newPogoInfoForAddr

	type PogoResp struct {
		Combined struct {
			UnpaidBalanceAtoms int `json:"UnpaidBalanceAtoms"`
			RecentPayouts      []struct {
				CreatedAt int `json:"CreatedAt"`
				Atoms     int `json:"Atoms"`
			} `json:"RecentPayouts"`
		} `json:"combined"`
	}

	var thisPogo PogoResp

	reqUrl := url.URL{
		Scheme: "https",
		Host:   "pogo.dmo-tools.com",
		Path:   "api/v1/stats/" + addr,
	}

	resp, err := http.Get(reqUrl.String())
	if err != nil {
		log.Printf("Unable to make request to dmo-tools: %s", err.Error())
		return
	}
	bodyText, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Unable to make request to dmo-tools: %s", err.Error())
		return
	}

	if err := json.Unmarshal(bodyText, &thisPogo); err != nil {
		log.Printf("Unable to make request to dmo-statservice: %s", err.Error())
		return
	}

	// This is where I should populate
	for _, payout := range thisPogo.Combined.RecentPayouts {
		pogoCoins[addr].coinsAtTimes[int64(payout.CreatedAt)] = float64(payout.Atoms) / 100000000
	}

}

// Load blocks from node up to current block. Do not expose RPC server until this is done. Display some output to user
func updateStats() {
	var err error

	currentHeight, err = getCurrentHeight()

	var noNode = false
	if err != nil {
		currentHeight = currentDBHeight
		fmt.Printf("Unable to connect to node, using cached DB data\n")
		noNode = true
	} else {

		fmt.Printf("Current block height from node: %d\n", currentHeight)
	}

	type DBResult struct {
		HeightID int `json:"height_id"`
	}

	getDBHeight()

	results, err := db.Query("select height_id from stats order by height_id asc limit 0,1")
	if err != nil {
		panic(err.Error())
	}

	for results.Next() {
		var dbResult DBResult
		err = results.Scan(&dbResult.HeightID)
		if err != nil {
			panic(err.Error()) // proper error handling instead of panic in your app
		}
		lowestDBHeight = dbResult.HeightID
	}

	var startHeight = currentDBHeight

	if startHeight < (currentHeight - blockHistoryDepth) {
		startHeight = currentHeight - blockHistoryDepth
	} else {
		startHeight = currentDBHeight + 1
	}

	if noNode {
		return
	}

	netHash, err2 := getCurrentNethash()
	if err2 == nil {
		globalNetHash = netHash
	}

	blockIDToGet := startHeight
	fmt.Printf("Grabbing %d new blocks from node...\n", currentHeight-blockIDToGet)

	var myBlockInfo blockInformation
	for blockIDToGet < currentHeight {
		myBlockInfo = getFullBlockInfoForHeight(blockIDToGet)
		mutex.Lock()
		blockMap[myBlockInfo.Height] = myBlockInfo // Add to memory cache
		mutex.Unlock()
		insert, err := db.Query(`
			INSERT INTO stats (height_id, blockhash, epoch, coins, miningaddr) VALUES (?, ?, ?, ?, ?)`,
			blockIDToGet, myBlockInfo.Hash, myBlockInfo.Time, myBlockInfo.Coins, myBlockInfo.Addr)

		if err != nil {
			panic(err.Error())
		}
		insert.Close()

		blockIDToGet++
		if (blockIDToGet % 500) == 0 {
			fmt.Printf("Grabbed up to block id %d\n", blockIDToGet)
		}
	}
	fmt.Printf("DB update from Node is complete!\n")

}

type mineRPC struct {
	Addresses string
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
}
*/
// TODO: Do not allow more than 10 receiving addresses
// For each addr, call getPogoInfoForAddr if the last time it was updated for that addr is before the beginning of the current hour
// When adding up coin counts, include the pogo info.
func getAddrMiningStatsRPC(c *gin.Context) {
	var jsonBody mineRPC

	if err := c.BindJSON(&jsonBody); err != nil {
		fmt.Printf("Got unhandled (bad) request!")
		return
	}

	type HourStat struct {
		Hour       int
		Coins      float64
		ChainCoins float64
		WinPercent float64
	}

	var hourStats []HourStat
	ipFrom := c.ClientIP()
	fmt.Printf("Request from %s: Getting stats for addresse(s) %s\n", ipFrom, jsonBody.Addresses)

	addrsToCheck := strings.Split(jsonBody.Addresses, ",")
	for i := 0; i < len(addrsToCheck); i++ {
		getPogoInfoForAddr(addrsToCheck[i])
	}

	hoursToday := getCurrentHour()
	for i := 0; i <= hoursToday; i++ {
		curHour := i - hoursToday
		startEpoch := getHourStart(curHour)
		endEpoch := startEpoch + 3600
		var thisHour HourStat

		thisHour.Coins = getCoinsInEpochRange(startEpoch, endEpoch, jsonBody.Addresses)
		thisHour.ChainCoins = getCoinsInEpochRange(startEpoch, endEpoch, "")
		if thisHour.Coins > 0.1 && thisHour.ChainCoins > 0.1 {
			thisHour.WinPercent = thisHour.Coins * 100.0 / thisHour.ChainCoins
		} else {
			thisHour.WinPercent = 0.0
		}
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

	numDays := jsonBody.NumDays
	if numDays < 2 {
		numDays = 2
	}
	if numDays > 21 {
		numDays = 21
	}
	for i := 0; i <= numDays; i++ {
		curDay := i - numDays
		startEpoch := getDayStart(curDay)
		endEpoch := startEpoch + 86400
		var thisDay DayStat

		thisDay.Coins = getCoinsInEpochRange(startEpoch, endEpoch, jsonBody.Addresses)
		thisDay.ChainCoins = getCoinsInEpochRange(startEpoch, endEpoch, "")

		if thisDay.Coins > 0.1 && thisDay.ChainCoins > 0.1 {
			thisDay.WinPercent = thisDay.Coins * 100.0 / thisDay.ChainCoins
		} else {
			thisDay.WinPercent = 0.0
		}
		formattedTime := time.Unix(startEpoch, 0).UTC().Format("2006-01-02")
		if i < numDays {
			thisDay.Day = formattedTime
		} else {
			thisDay.Day = "Today"
		}
		dayStats = append(dayStats, thisDay)
	}

	type ResponseStats struct {
		HourlyStats         []HourStat
		DailyStats          []DayStat
		ProjectedCoinsToday float64
		NetHash             float64
	}

	secondsSoFarToday := float64(time.Now().Unix()-getDayStart(0)) + 1.0

	var thisResponse ResponseStats
	thisResponse.NetHash = globalNetHash
	thisResponse.ProjectedCoinsToday = dayStats[len(dayStats)-1].Coins * (86400.0 / secondsSoFarToday)
	thisResponse.HourlyStats = hourStats
	thisResponse.DailyStats = dayStats

	c.JSON(200, thisResponse)
}

// Get lowest and highest block for epoch range
func findBlocksForEpochRange(startEpoch int64, endEpoch int64) (int, int) {
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

	return lowest, highest
}

// Get the number of coins in a given epoch range. If addresses is passed, limit count to coins
// for those addresses. If not then just get all mined coins in range count...
func getCoinsInEpochRange(startEpoch int64, endEpoch int64, addresses string) float64 {
	addrsToCheck := strings.Split(addresses, ",")
	numCoins := 0.0

	lowest, highest := findBlocksForEpochRange(startEpoch, endEpoch)

	// Add POGO counts for this epoch range here
	for j := 0; j < len(addrsToCheck); j++ {
		for pogoEpoch, coins := range pogoCoins[addrsToCheck[j]].coinsAtTimes {
			if startEpoch <= pogoEpoch && pogoEpoch < endEpoch {
				numCoins += coins
			}
		}
	}

	for i := lowest; i < highest; i++ {
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

func getFullBlockInfoForHeight(height int) blockInformation {
	var myBlockInfo blockInformation
	myBlockInfo.Height = height
	myBlockInfo = getBlockHash(myBlockInfo)
	myBlockInfo = getBlock(myBlockInfo)
	myBlockInfo = getTransInfo(myBlockInfo)
	return myBlockInfo
}

func getCurrentNethash() (float64, error) {
	type netHashResult struct {
		Result float64 `json:"result"`
	}

	client := &http.Client{
		Timeout: 5 * time.Second,
	}
	reqURL := url.URL{
		Scheme: "http",
		Host:   c.NodeIP + ":" + c.NodePort,
		Path:   "",
	}

	var data = bytes.NewBufferString(`{"id": 1,"method": "getnetworkhashps","params": {"nblocks": 100}}`)
	req, err := http.NewRequest("POST", reqURL.String(), data)
	if err != nil {
		return 0, err
	}

	req.SetBasicAuth(c.NodeUser, c.NodePass)
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	bodyText, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	var myNetHash netHashResult

	if err := json.Unmarshal(bodyText, &myNetHash); err != nil {
		return 0, err
	}
	return myNetHash.Result, nil
}

func getCurrentHeight() (int, error) {
	type blockHeightResult struct {
		ID     string `json:"id"`
		Result int    `json:"result"`
	}

	client := &http.Client{
		Timeout: 5 * time.Second,
	}
	reqURL := url.URL{
		Scheme: "http",
		Host:   c.NodeIP + ":" + c.NodePort,
		Path:   "",
	}

	var data = bytes.NewBufferString(`{"jsonrpc":"1.0","id":"curltest","method":"getblockcount", "params": { }}`)
	req, err := http.NewRequest("POST", reqURL.String(), data)
	if err != nil {
		return 0, err
	}

	req.SetBasicAuth(c.NodeUser, c.NodePass)
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	bodyText, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	var myBlockHeight blockHeightResult

	if err := json.Unmarshal(bodyText, &myBlockHeight); err != nil {
		return 0, err
	}
	return myBlockHeight.Result, nil
}

// Step one, get the block hash for the block number
func getBlockHash(blockInfo blockInformation) blockInformation {

	type blockHashResult struct {
		ID     string `json:"id"`
		Result string `json:"result"`
	}

	client := &http.Client{}
	reqURL := url.URL{
		Scheme: "http",
		Host:   c.NodeIP + ":" + c.NodePort,
		Path:   "",
	}

	var data = bytes.NewBufferString(`{"jsonrpc":"1.0","id":"curltest","method":"getblockhash", "params": { "height": ` + strconv.Itoa(blockInfo.Height) + `}}`)
	req, err := http.NewRequest("POST", reqURL.String(), data)
	if err != nil {
		log.Fatalf("Unable to construct getblockhash request to %q: %s", reqURL.String(), err)
	}

	req.SetBasicAuth(c.NodeUser, c.NodePass)
	resp, err := client.Do(req)
	if err != nil {
		log.Fatal(err)
		return blockInfo
	}
	bodyText, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("Unable to read response body for getblockhash request: %s", err)
	}

	var myBlockHash blockHashResult

	if err := json.Unmarshal(bodyText, &myBlockHash); err != nil {
		return blockInfo
	}
	blockInfo.Hash = myBlockHash.Result

	return blockInfo
}

// Step two, get the block for the hash... returns a txid IF IT WAS A MINED BLOCK
func getBlock(blockInfo blockInformation) blockInformation {
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
	reqURL := url.URL{
		Scheme: "http",
		Host:   c.NodeIP + ":" + c.NodePort,
		Path:   "",
	}

	var data = bytes.NewBufferString(`{"jsonrpc":"1.0","id":"curltest","method":"getblock", "params": { "blockhash": "` + blockInfo.Hash + `"}}`)
	req, err := http.NewRequest("POST", reqURL.String(), data)
	if err != nil {
		log.Fatalf("Unable to construct getblock request to %q: %s", reqURL.String(), err)
	}
	req.SetBasicAuth(c.NodeUser, c.NodePass)
	resp, err := client.Do(req)
	if err != nil {
		log.Fatal(err)
		return blockInfo
	}

	bodyText, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("Unable to read response body for getblock request: %s", err)
	}

	var myBlock blockResult

	if err := json.Unmarshal(bodyText, &myBlock); err != nil {
		return blockInfo
	}

	blockInfo.Time = myBlock.Result.Time
	blockInfo.TxID = myBlock.Result.Tx[0]
	return blockInfo
}

type minedTxInfo struct {
	miningAddr string
	coins      float64
}

// Step three, get the information I care about
func getTransInfo(blockInfo blockInformation) blockInformation {

	var myInfo minedTxInfo

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
	reqURL := url.URL{
		Scheme: "http",
		Host:   c.NodeIP + ":" + c.NodePort,
		Path:   "",
	}

	var data = bytes.NewBufferString(`{"jsonrpc":"1.0","id":"curltest","method":"getrawtransaction", "params": { "blockhash": "` + blockInfo.Hash + `", "txid": "` + blockInfo.TxID + `", "verbose": true}}`)
	req, err := http.NewRequest("POST", reqURL.String(), data)
	if err != nil {
		log.Fatalf("Unable to construct getrawtransaction request to %q: %s", reqURL.String(), err)
	}

	req.SetBasicAuth(c.NodeUser, c.NodePass)
	resp, err := client.Do(req)
	if err != nil {
		log.Fatal(err)
		return blockInfo
	}
	bodyText, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("Unable to read getrawtransaction body: %s", err)
	}

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
