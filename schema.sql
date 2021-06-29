-- extension --
create extension pgcrypto;

-- user --
create table member
(
    id serial not null
        constraint member_pk
            primary key,
    email varchar not null,
    password varchar not null,
    name varchar not null,
    status smallint default 1 not null,
    created_at timestamptz default current_timestamp not null,
    updated_at timestamptz
);

create unique index member_email_uindex
    on member (email);

-- record --
create table record
(
    id bigserial not null
        constraint record_pk
            primary key,
    member_id int not null
        constraint record_member_id_fk
            references member,
    type smallint not null,
    status smallint default 1 not null,
    created_at timestamptz default current_timestamp not null,
    updated_at timestamptz
);

