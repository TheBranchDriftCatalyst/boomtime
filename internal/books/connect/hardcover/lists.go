package hardcover

import (
	"context"
	"encoding/json"
	"strings"
)

// lists.go — pull the user's Hardcover LISTS (Guilty Pleasures, Owned, Hard Sci
// Fi, …) + their membership so boomtime can attach a book's list names as a
// property on its reading_items (migration 00077). Read-only. The GraphQL shape
// is VERIFIED against the live API (2026-08, user VoidDiplomacy), not guessed:
//
//	lists(where:{user_id:{_eq}}, offset, limit) {
//	  id name books_count
//	  list_books { book_id }
//	}
//
// Lists are small (typically single-digit to low-dozens of books), so we page the
// OUTER lists and request each list's book_ids in one shot (listBooksCap). If a
// list ever exceeds the cap the tail is dropped — logged by the caller via
// books_count vs len — acceptable for v1.

// userListsPageSize is the offset/limit page size for the outer lists sweep.
const userListsPageSize = 50

// UserList is one Hardcover list plus the book ids it contains.
type UserList struct {
	ID         int
	Name       string
	BooksCount int
	BookIDs    []int64
}

// list_books(limit: 2000) — an inline LITERAL, not a $variable: Hardcover's
// server-side Typesense/Hasura args can silently mishandle bound variables (the
// search bug gaka-nq2m), so per-list membership uses a generous literal cap. Real
// lists are tiny, so the tail is never dropped in practice.
const userListsQuery = `query Lists($u: Int!, $o: Int!, $l: Int!) {
  lists(where: {user_id: {_eq: $u}}, order_by: {id: asc}, offset: $o, limit: $l) {
    id
    name
    books_count
    list_books(limit: 2000) { book_id }
  }
}`

// UserLists fetches all of the user's lists (+ their book ids), paging the outer
// lists to exhaustion. READ-ONLY.
func (c *Client) UserLists(ctx context.Context, userID int) ([]UserList, error) {
	var out []UserList
	for offset := 0; ; offset += userListsPageSize {
		var data struct {
			Lists []struct {
				ID         int    `json:"id"`
				Name       string `json:"name"`
				BooksCount int    `json:"books_count"`
				ListBooks  []struct {
					BookID int64 `json:"book_id"`
				} `json:"list_books"`
			} `json:"lists"`
		}
		if err := c.graphql(ctx, userListsQuery, map[string]any{
			"u": userID, "o": offset, "l": userListsPageSize,
		}, &data); err != nil {
			return nil, err
		}
		for _, l := range data.Lists {
			ul := UserList{ID: l.ID, Name: strings.TrimSpace(l.Name), BooksCount: l.BooksCount}
			for _, lb := range l.ListBooks {
				if lb.BookID > 0 {
					ul.BookIDs = append(ul.BookIDs, lb.BookID)
				}
			}
			out = append(out, ul)
		}
		if len(data.Lists) < userListsPageSize {
			break // short page → all lists fetched
		}
	}
	return out, nil
}

// listMembershipByBook inverts the lists into a book_id → sorted-unique list-names
// map (the shape the pull attaches per book). Names are de-duplicated per book.
func listMembershipByBook(lists []UserList) map[int64][]string {
	seen := map[int64]map[string]bool{}
	for _, l := range lists {
		if l.Name == "" {
			continue
		}
		for _, bid := range l.BookIDs {
			if seen[bid] == nil {
				seen[bid] = map[string]bool{}
			}
			seen[bid][l.Name] = true
		}
	}
	out := make(map[int64][]string, len(seen))
	for bid, names := range seen {
		list := make([]string, 0, len(names))
		for n := range names {
			list = append(list, n)
		}
		out[bid] = list
	}
	return out
}

// marshalLists JSON-encodes a book's list names for the hardcover_lists jsonb
// column. An empty slice encodes to "[]" (the book is on no list).
func marshalLists(names []string) []byte {
	if len(names) == 0 {
		return []byte("[]")
	}
	b, err := json.Marshal(names)
	if err != nil {
		return []byte("[]")
	}
	return b
}
