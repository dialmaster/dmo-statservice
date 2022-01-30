
Setup:
1) You will need to install https://github.com/pressly/goose to support the DB migrations needed to setup your database for this service:
go install github.com/pressly/goose/v3/cmd/goose@latest

2) Install mysql and create a database for this data to live in, create a user with full access to read/write within that DB.
3) Copy migrations/goose_up.example.sh to migration/goose_up.sh, update the script with your database/user information
4) Run goose_up.sh from the migrations directory
5) go build
6) Copy config.yaml to myconfig.yaml and update it with your information
7) Run 
