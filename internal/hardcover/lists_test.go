package hardcover

import (
	"reflect"
	"sort"
	"testing"
)

func TestListMembershipByBook(t *testing.T) {
	lists := []UserList{
		{Name: "Owned", BookIDs: []int64{1, 2, 3}},
		{Name: "Hard Sci Fi", BookIDs: []int64{2, 3}},
		{Name: "", BookIDs: []int64{9}},          // unnamed list → skipped
		{Name: "Favorites", BookIDs: []int64{2}}, // book 2 on three lists
	}
	got := listMembershipByBook(lists)

	// book 2 is on Owned + Hard Sci Fi + Favorites (de-duped, order-independent).
	names := got[2]
	sort.Strings(names)
	if !reflect.DeepEqual(names, []string{"Favorites", "Hard Sci Fi", "Owned"}) {
		t.Errorf("book 2 lists = %v, want the three", names)
	}
	if len(got[1]) != 1 || got[1][0] != "Owned" {
		t.Errorf("book 1 = %v, want [Owned]", got[1])
	}
	if _, ok := got[9]; ok {
		t.Errorf("book 9 (unnamed-list-only) should not appear")
	}
}

func TestMarshalLists(t *testing.T) {
	if string(marshalLists(nil)) != "[]" {
		t.Errorf("nil → want []")
	}
	if got := string(marshalLists([]string{"Owned", "Art"})); got != `["Owned","Art"]` {
		t.Errorf("got %s", got)
	}
}
