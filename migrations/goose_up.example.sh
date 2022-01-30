#!/bin/bash
goose mysql "username:password@tcp(127.0.0.1:3306)/DBNAME?parseTime=true" up
