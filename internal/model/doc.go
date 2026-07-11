// Package model holds the domain structs and their JSON wire representations.
// JSON tags reproduce hakatime's aeson field-name rules exactly:
//   - noPrefixOptions: drop the leading lowercase prefix and lowercase the next
//     char (pName -> name, tGrand_total -> grand_total, leadProject -> project).
//   - convertReservedWords (heartbeat only): ty->type, time_sent->time,
//     file_lines->lines; all other fields keep their name.
package model
