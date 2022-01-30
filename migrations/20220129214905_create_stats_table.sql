-- +goose Up
-- +goose StatementBegin
create table stats
 (
  height_id int not null primary key,
  blockhash varchar(64),
  epoch int(11) unsigned not null,
  coins float,
  miningaddr varchar(64)
  

 )engine=innodb;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
drop table stats;
-- +goose StatementEnd
