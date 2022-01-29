package main

import (
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"log"
	"os"
)

type conf struct {
	NodeIP             string  `yaml:"NodeIP"`
	NodePort           string  `yaml:"NodePort"`
	NodeUser           string  `yaml:"NodeUser"`
	NodePass           string  `yaml:"NodePass"`
}

func (c *conf) getConf() *conf {
	myConfigFile := "config.yaml"
	if _, err := os.Stat("myconfig.yaml"); err == nil {
		myConfigFile = "myconfig.yaml"
	}

	yamlFile, err := ioutil.ReadFile(myConfigFile)
	if err != nil {
		log.Printf("yamlFile.Get err   #%v ", err)
	}
	err = yaml.Unmarshal(yamlFile, c)
	if err != nil {
		log.Fatalf("Unmarshal: %v", err)
	}

	return c
}
