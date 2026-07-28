# Zoekt Query Language Guide

This guide explains the Zoekt query language, used for searching text within Git repositories. Zoekt queries allow combining multiple filters and expressions using logical operators, negations, and grouping. Here's how to craft queries effectively.

For a brief overview of Zoekt's query syntax, see [these great docs from neogrok](https://neogrok-demo-web.fly.dev/syntax).

---

## Syntax Overview

A query is made up of expressions. An **expression** can be:
- A search pattern (e.g., `error.*handler`),
- A field (e.g., `repo:`),
- A grouping (e.g., parentheses `()`),
- A negated expression (e.g., `-repo:archived-project`).

The lowercase `or` operator combines alternatives. Conjunction is implicit,
so multiple expressions written together are treated as AND conditions.

---

## Query Components

### 1. **Fields**

Fields restrict your query to specific criteria. Here's a list of fields and their usage:

| Field        | Aliases | Values                 | Description                                                | Examples                               |
|--------------|---------|------------------------|------------------------------------------------------------|----------------------------------------|
| `archived:`  |         | `yes` or `no`          | Filters archived repositories.                             | `archived:yes`                         |
| `case:`      |         | `yes`, `no`, or `auto` | Matches case-sensitive or insensitive text.                | `case:yes content:"Foo"`               |
| `content:`   | `c:`    | Regex pattern          | Searches content of files.                                 | `content:"search term"`                |
| `file:`      | `f:`    | Regex pattern          | Searches file names.                                       | `file:main\.go$`                       |
| `fork:`      |         | `yes` or `no`          | Filters forked repositories.                               | `fork:no`                              |
| `lang:`      |         | Text                   | Filters by programming language.                           | `lang:python`                          |
| `meta.<field>:` |      | Regex pattern          | Filters repository metadata values.                        | `meta.license:Apache-.*`               |
| `public:`    |         | `yes` or `no`          | Filters public repositories.                               | `public:yes`                           |
| `regex:`     |         | Regex pattern          | Matches content using a regular expression.                | `regex:foo.*bar`                       |
| `repo:`      | `r:`    | Regex pattern          | Filters repositories by name.                              | `repo:github\.com/user/project$`       |
| `sym:`       |         | Regex pattern          | Searches for symbol names.                                 | `sym:"MyFunction"`                     |
| `branch:`    | `b:`    | Text                   | Matches branch names containing the value; `HEAD` selects the default branch. | `branch:main` |
| `type:`      | `t:`    | `filematch`, `filename`, `file`, or `repo` | Limits result types.                   | `type:filematch`                       |

---

### 2. **Negation**

Negate an expression using the `-` symbol.

#### Examples:
- Exclude a repository:
  ```plaintext
  -repo:github\.com/example/repo$
  ```
- Exclude a language:
  ```plaintext
  -lang:javascript
  ```

---

### 3. **Grouping**

Group queries using parentheses `()` to create complex logic.

#### Examples:
- Match either of two repositories:
  ```plaintext
  (repo:repo1 or repo:repo2)
  ```
- Find test in either python or javascript files:
  ```plaintext
  content:test (lang:python or lang:javascript)
  ```

---

### 4. **Logical Operators**

Use `or` to combine multiple expressions.

#### Examples:
- Match files in either of two languages:
  ```plaintext
  lang:go or lang:java
  ```

Conjunction is applied automatically when expressions are separated by a
space. The word `and` is not an operator; it is interpreted as a search term.

---

## Special Query Types

### Filtering by Repository Type

Zoekt supports filtering repositories by various attributes:

```plaintext
public:yes archived:no fork:no
```

This finds repositories that are public, not archived, and not forks.

### Filtering by Repository Metadata

When an indexer stores custom key-value metadata for a repository, use
`meta.<field>:` to match the value with a regular expression. For example:

```plaintext
meta.license:Apache-.*
```

Repositories without that metadata field do not match.

### Result Type Control

The `type:` operator controls what kind of results are returned:

```plaintext
type:repo content:config
```

This returns repository names instead of file matches. Valid values include:
- `filematch` - Returns file content matches (default)
- `filename` (or `file`) - Returns only matching filenames
- `repo` - Returns only repository names

`type:` applies to the whole expression in its current scope, including `or`
clauses. For example, `type:repo foo or bar` is equivalent to
`type:repo (foo or bar)`. Use parentheses to scope `type:` to only one branch,
for example `(type:repo foo) or bar`.

---

## Special Query Values

- **Boolean Values**:
  Use `yes` or `no` for fields like `archived:` or `fork:`.

- **Text Fields**:
  Search terms and text fields (`content:`, `repo:`, etc.) are parsed as
  [Go regular expressions](https://pkg.go.dev/regexp/syntax). Patterns that
  contain no regular expression operations are optimized as substring
  searches. Write patterns directly without surrounding `/` delimiters;
  slashes in a pattern are matched literally.

- **Quoted Values**:
  Double quotes group values containing spaces, as in `"my text"`. Inside a
  quoted value, a backslash escapes the next character. Use two backslashes
  when the regular expression itself needs a backslash.

- **Escape Characters**:
  To include special characters, use backslashes (`\`).

#### Examples:
- Match the string `foo"bar`:
  ```plaintext
  content:"foo\"bar"
  ```
- Match the regex `foo.*bar`:
  ```plaintext
  content:foo.*bar
  ```

---

## Case Sensitivity

Zoekt supports three case sensitivity modes:

- `case:yes` - Exact case matching
- `case:no` - Case-insensitive matching
- `case:auto` - Automatically detect based on pattern (default)

In auto mode, if the pattern contains uppercase letters, the search will be
case-sensitive; otherwise, it will be case-insensitive.

---

## Advanced Examples

1. **Search for content in Python files in public repositories**:
   ```plaintext
   lang:python public:yes content:"my_function"
   ```

2. **Exclude archived repositories and match a regex**:
   ```plaintext
   archived:no regex:error.*handler
   ```

3. **Find files named `README.md` in forks**:
   ```plaintext
   file:README\.md$ fork:yes
   ```

4. **Search for a specific branch**:
   ```plaintext
   branch:main content:"TODO"
   ```

5. **Combine multiple fields**:
   ```plaintext
   (repo:github\.com/example$ or repo:github\.com/test$) lang:go
   ```

---

## Tips

1. **Combine Filters**: You can combine as many fields as needed. For instance:
   ```plaintext
   repo:github\.com/example$ lang:go content:"init"
   ```

2. **Use Regular Expressions**: Make complex content searches more powerful:
   ```plaintext
   content:func\s+\w+\s*\(
   ```

3. **Case Sensitivity**: Use `case:yes` for exact matches:
   ```plaintext
   case:yes content:"ExactMatch"
   ```

4. **Match Specific File Types**:
   ```plaintext
   file:.*\.go$ content:"package main"
   ```

### EBNF Summary

```ebnf
query       = conjunction , { "or" , conjunction } ;

conjunction = expression , { expression } ;

expression  = [ "-" ] , ( grouping | text | field ) ;

grouping    = "(" , query , ")" ;

field       = ( "archived:" , boolean )
            | ( "case:" , ("yes" | "no" | "auto") )
            | ( ( "content:" | "c:" ) , text )
            | ( ( "file:" | "f:" ) , text )
            | ( "fork:" , boolean )
            | ( "lang:" , text )
            | ( "public:" , boolean )
            | ( "regex:" , text )
            | ( ( "repo:" | "r:" ) , text )
            | ( "sym:" , text )
            | ( ( "branch:" | "b:" ) , text )
            | ( ( "type:" | "t:" ) , type )
            | ( "meta." , metadata-name , ":" , text );

boolean     = "yes" | "no" ;
text        = quoted | unquoted ;
quoted      = '"' , { character | escape } , '"' ;
unquoted    = character , { character | escape } ;

type        = "filematch" | "filename" | "file" | "repo" ;
```
