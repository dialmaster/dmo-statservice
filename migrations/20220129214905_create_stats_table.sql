-- +goose Up
-- +goose StatementBegin
create table stats
 (
  height_id int not null primary key,
  blockhash varchar(64) not null,
  epoch int(11) unsigned not null,
  coins float

 )engine=innodb;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
drop table stats;
-- +goose StatementEnd
