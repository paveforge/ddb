// Package ddbkit prepares DynamoDB tables and loads fixture files synchronously.
// Callers supply a DynamoDB client and own table cleanup. Load overwrites matching
// items by default; Truncate clears all records before loading. Operations are not
// atomic and require exclusive access when an exact dataset is needed. Use
// strongly consistent base-table reads after loading; secondary index assertions
// may require polling. Bound the entire operation with a context deadline.
package ddbkit
