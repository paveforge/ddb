# IAM permissions

Scope policies to the table ARNs and region/account you intend to use. Read-only
preflight does not prove write authorization. Export needs `dynamodb:Scan`;
import into an existing table needs `dynamodb:DescribeTable` and
`dynamodb:BatchWriteItem`. Truncation also needs `dynamodb:Scan`. Optional table
creation needs `dynamodb:CreateTable`; definitions containing tags need
`dynamodb:TagResource`. Encryption and other advanced settings may require
additional permissions and key policies.

Example for import and optional truncation into a single existing table (replace
the example region/account/table):

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "dynamodb:DescribeTable",
        "dynamodb:BatchWriteItem",
        "dynamodb:Scan"
      ],
      "Resource": "arn:aws:dynamodb:us-east-1:123456789012:table/users-dev"
    }
  ]
}
```

Remove Scan if truncation is unnecessary. For export-only access use Scan alone.
Add CreateTable and, when needed, TagResource for the exact destination ARN when
using `--table-file`. The CLI does not request table deletion or schema updates.
