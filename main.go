package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

var c conf


type BlockInformation struct {
	Time int
	Height int
	TxId string
	Hash string
	Addr string
	Coins float64
}

// Get the last X blocks and print out a set of stats for addresses and the number of coins mined
func main() {
	c.getConf()

	miningInfo := make(map[string]float64)

	blockbase := 1160949
	var myBlockInfo BlockInformation
	n := 1
	var firstTime, lastTime int
	var numblocks = 2000

	for n <= numblocks {
		
		myBlockInfo = getFullBlockInfoForHeight(blockbase + n)
		if (n == 1) {
			firstTime = myBlockInfo.Time
		}
		if (n == (numblocks-1)) {
			lastTime = myBlockInfo.Time
		}

		miningInfo[myBlockInfo.Addr] += myBlockInfo.Coins
    	n += 1
	}

	fmt.Printf("First Time is %s\n", epochToString(firstTime))
	fmt.Printf("Last Time is %s\n", epochToString(lastTime))

	//for key, element := range miningInfo {
    //    fmt.Println("Addr:", key, "=>", "Coins:", element)
    //}


	fmt.Printf("MY COINS IN RANGE: %.2f\n", miningInfo["dy1qpfr5yhdkgs6jyuk945y23pskdxmy9ajefczsvm"])
	fmt.Printf("My percent: %.2f\n", miningInfo["dy1qpfr5yhdkgs6jyuk945y23pskdxmy9ajefczsvm"] * 100 / float64(numblocks))
}

func epochToString(epoch int) string {
	unixTimeUTC:=time.Unix(int64(epoch), 0) //gives unix time stamp in utc 
	unitTimeInRFC3339 :=unixTimeUTC.Format(time.RFC3339)

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


// Step one, get the block hash for the block number
func getBlockHash(blockInfo BlockInformation) BlockInformation {

	type blockHashResult struct {
		ID      string  `json:"id"`
		Result      string  `json:"result"`
		error      string  `json:"error"`
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
		ID    string         `json:"id"`
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
	
	blockInfo.Time =myBlock.Result.Time
	blockInfo.TxId = myBlock.Result.Tx[0]
	return blockInfo
}


type MinedTxInfo struct {
	miningAddr string
	coins float64
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
		ID    string         `json:"id"`
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



	blockInfo.Addr = myTrans.Result.Vout[0].ScriptPubKey.Address
	blockInfo.Coins = myTrans.Result.Vout[0].Value






	return blockInfo

}


