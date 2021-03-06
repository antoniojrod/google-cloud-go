// Copyright 2015 Google Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package bigquery

import (
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	"cloud.google.com/go/internal/testutil"
	"golang.org/x/net/context"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

func TestIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Integration tests skipped in short mode")
	}

	ctx := context.Background()
	ts := testutil.TokenSource(ctx, Scope)
	if ts == nil {
		t.Skip("Integration tests skipped. See CONTRIBUTING.md for details")
	}

	projID := testutil.ProjID()
	c, err := NewClient(ctx, projID, option.WithTokenSource(ts))
	if err != nil {
		t.Fatal(err)
	}
	ds := c.Dataset("bigquery_integration_test")
	if err := ds.Create(ctx); err != nil && !hasStatusCode(err, http.StatusConflict) { // AlreadyExists is 409
		t.Fatal(err)
	}
	schema := Schema([]*FieldSchema{
		{Name: "name", Type: StringFieldType},
		{Name: "num", Type: IntegerFieldType},
	})
	table := ds.Table("t1")
	// Delete the table in case it already exists. (Ignore errors.)
	table.Delete(ctx)
	// Create the table.
	err = table.Create(ctx, schema, TableExpiration(time.Now().Add(5*time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	// Check table metadata.
	md, err := table.Metadata(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// TODO(jba): check md more thorougly.
	if got, want := md.ID, fmt.Sprintf("%s:%s.%s", projID, ds.id, table.TableID); got != want {
		t.Errorf("metadata.ID: got %q, want %q", got, want)
	}
	if got, want := md.Type, RegularTable; got != want {
		t.Errorf("metadata.Type: got %v, want %v", got, want)
	}

	// List tables in the dataset.
	tableIter := ds.Tables(ctx)
	var tables []*Table
	for {
		table, err := tableIter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		tables = append(tables, table)
	}
	if got, want := len(tables), 1; got != want {
		t.Fatalf("ListTables: got %d, want %d", got, want)
	}
	want := *table
	if got := tables[0]; !reflect.DeepEqual(got, &want) {
		t.Errorf("ListTables: got %v, want %v", got, &want)
	}

	// Iterate over tables in the dataset.
	it := ds.Tables(ctx)
	tables = nil
	for {
		tbl, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		tables = append(tables, tbl)
	}
	if got, want := tables, []*Table{table}; !reflect.DeepEqual(got, want) {
		t.Errorf("Tables: got %v, want %v", got, want)
	}

	// Populate the table.
	upl := table.Uploader()
	var rows []*ValuesSaver
	for i, name := range []string{"a", "b", "c"} {
		rows = append(rows, &ValuesSaver{
			Schema:   schema,
			InsertID: name,
			Row:      []Value{name, i},
		})
	}
	if err := upl.Put(ctx, rows); err != nil {
		t.Fatal(err)
	}

	checkRead := func(it *RowIterator) {
		for i := 0; true; i++ {
			var vals ValueList
			err := it.Next(&vals)
			if err == iterator.Done {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if got, want := vals, rows[i].Row; !reflect.DeepEqual([]Value(got), want) {
				t.Errorf("got %v, want %v", got, want)
			}
		}
	}
	// Read the table.
	checkRead(table.Read(ctx))

	// Query the table.
	q := c.Query("select name, num from t1")
	q.DefaultProjectID = projID
	q.DefaultDatasetID = ds.id

	rit, err := q.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	checkRead(rit)

	// Query the long way.
	job1, err := q.Run(ctx)
	if err != nil {
		t.Fatal(err)
	}
	job2, err := c.JobFromID(ctx, job1.ID())
	if err != nil {
		t.Fatal(err)
	}
	// TODO(jba): poll status until job is done
	_, err = job2.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}

	rit, err = job2.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	checkRead(rit)

	// Test Update.
	tm, err := table.Metadata(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantDescription := tm.Description + "more"
	wantName := tm.Name + "more"
	got, err := table.Update(ctx, TableMetadataToUpdate{
		Description: wantDescription,
		Name:        wantName,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Description != wantDescription {
		t.Errorf("Description: got %q, want %q", got.Description, wantDescription)
	}
	if got.Name != wantName {
		t.Errorf("Name: got %q, want %q", got.Name, wantName)
	}
}

func hasStatusCode(err error, code int) bool {
	if e, ok := err.(*googleapi.Error); ok && e.Code == code {
		return true
	}
	return false
}
