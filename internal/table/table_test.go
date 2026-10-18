package table

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"testing"
)

func valid() *dynamodb.CreateTableInput {
	return &dynamodb.CreateTableInput{TableName: aws.String("users"), BillingMode: types.BillingModePayPerRequest, AttributeDefinitions: []types.AttributeDefinition{{AttributeName: aws.String("id"), AttributeType: types.ScalarAttributeTypeS}}, KeySchema: []types.KeySchemaElement{{AttributeName: aws.String("id"), KeyType: types.KeyTypeHash}}}
}
func TestDefinitionAndCompatibility(t *testing.T) {
	v := valid()
	if e := Validate(v); e != nil {
		t.Fatal(e)
	}
	d := FromDefinition(v)
	if e := Compatible(v, d); e != nil {
		t.Fatal(e)
	}
	d.AttributeDefinitions = append([]types.AttributeDefinition{}, d.AttributeDefinitions...)
	d.AttributeDefinitions[0].AttributeType = types.ScalarAttributeTypeN
	if e := Compatible(v, d); e == nil {
		t.Fatal("accepted incompatible key")
	}
	v.KeySchema = append(v.KeySchema, v.KeySchema[0])
	if e := Validate(v); e == nil {
		t.Fatal("accepted duplicate key")
	}
}
