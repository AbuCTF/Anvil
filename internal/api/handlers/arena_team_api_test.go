package handlers

import (
	"encoding/json"
	"testing"
)

func TestMeStatusServiceJSONContract(t *testing.T) {
	port := 9001
	fate := "held"
	got, err := json.Marshal(meServiceRow{
		ServiceID: "service-id",
		Slug:      "service",
		Name:      "Service",
		Port:      &port,
		Stores: []meStoreRow{{
			StoreIndex: 0,
			StoreName:  "flag 1",
			Steal:      1,
			Hold:       2,
			Up:         3,
			Ticks:      []meTickRow{{Tick: 4, Status: "OK", FlagFate: &fate}},
		}},
		PointsThisHour:  mePointsHour{Steal: 1, Hold: 2, Up: 3},
		ActiveAttackers: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"service_id":"service-id","slug":"service","name":"Service","port":9001,"stores":[{"store_index":0,"store_name":"flag 1","steal":1,"hold":2,"up":3,"ticks":[{"tick":4,"status":"OK","flag_fate":"held"}]}],"points_this_hour":{"steal":1,"hold":2,"up":3},"active_attackers":1}`
	if string(got) != want {
		t.Fatalf("me status JSON = %s", got)
	}
}
