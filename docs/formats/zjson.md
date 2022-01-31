# Zed over JSON (ZJSON)

* [1. Introduction](#1-introduction)
* [2. The Format](#2-the-formant)
  + [2.1 Type Encoding](#21-type-encoding)
    - [2.1.1 Record Type](#211-record-type)
    - [2.1.2 Array Type](#212-array-type)
    - [2.1.3 Set Type](#213-set-type)
    - [2.1.4 Map Type](#214-map-type)
    - [2.1.5 Union type](#215-union-type)
    - [2.1.6 Enum Type](#216-enum-type)
    - [2.1.7 Error Type](#217-error-type)
    - [2.1.8 Named Type](#218-named-type)
  + [2.2 Value Encoding](#22-value-encoding)
* [3. Object Framing](#3-object-framing)
* [4. Example](#4-example)

## 1. Introduction

The [Zed data model](zed.md)
is based on richly typed records with a deterministic column order,
as is implemented by the [ZSON](zson.md), [ZNG](zng.md), and [ZST](zst.md) formats.
Given the ubiquity of JSON, it is desirable to also be able to serialize
Zed data into the JSON format.   However, encoding Zed data values
directly as JSON values would not work without loss of information.

For example, consider this Zed data as [ZSON](zson.md):
```
{
    ts: 2018-03-24T17:15:21.926018012Z,
    a: "hello, world",
    b: {
        x: 4611686018427387904,
        y: 127.0.0.1
    }
}
```
A straightforward translation to JSON might look like this:
```
{
  "ts": 1521911721.926018012,
  "a": "hello, world",
  "b": {
    "x": 4611686018427387904,
    "y": "127.0.0.1"
  }
}
```
But, when this JSON is transmitted to a JavaScript client and parsed,
the result looks something like this:
```
{
  "ts": 1521911721.926018,
  "a": "hello, world",
  "b": {
    "x": 4611686018427388000,
    "y": "127.0.0.1"
  }
}
```
The good news is the `a` field came through just fine, but there are
a few problems with the remaining fields:
* the timestamp lost precision (due to 53 bits of mantissa in a JavaScript
IEEE 754 floating point number) and was converted from a time type to a number,
* the int64 lost precision for the same reason, and
* the IP address has been converted to a string.

As a comparison, Python's `json` module handles the 64-bit integer to full
precision, but loses precision on the floating point timestamp.
Also, as mentioned, it is at the whim of a JSON implementation whether
or not the order of object keys is preserved.

While JSON is well suited for data exchange of generic information, it is not
so appropriate for a [super-structured data model](zed.md#2-zed-a-super-structured-pattern)
like Zed.  That said, JSON can be used as an encoding format for Zed by mapping Zed data
onto a JSON-based protocol.  This allows clients like web apps or
Electron apps to receive and understand Zed and, with the help of client
libraries like [Zealot](https://github.com/brimdata/zealot),
to manipulate the rich, structured Zed types that are implemented on top of
the basic JavaScript types.

In other words,
because JSON objects do not have a deterministic column order nor does JSON
in general have typing beyond the basics (i.e., strings, floating point numbers,
objects, arrays, and booleans), we decided to encode Zed data with
its embedded type model all in a layer above regular JSON.

## 2. The Format

The format for representing Zed in JSON is called ZJSON.
Converting ZSON, ZNG, or ZST to ZJSON and back results in a complete and
accurate restoration of the original Zed data.

A ZJSON stream is defined as a sequence of JSON objects where each object
represents a Zed value and has the form:
```
{
  "type": <type>,
  "value": <value>
}
```
The type and value fields are encoded as defined below.

### 2.1 Type Encoding

The type encoding for a primitive type is simply its [Zed type name](zed.md#1-primitive-types)
e.g., "int32" or "string".

Complex types are encoded with small-integer identifiers.
The first instance of a unique type defines the binding between the
integer identifier and its definition, where the definition may recursively
refer  to earlier complex types by their identifiers.

For example, the Zed type `{s:string,x:int32}` has this ZJSON format:
```
{
  id: 123,
  "kind": "record",
  "fields": [
    {
      "name": "s",
      "type": {
        "kind": "primitive",
        "name": "string"
      }
    },
    {
      "name": "x",
      "type": {
        "kind": "primitive",
        "name": "int64"
      }
    }
  ]
}
```

#### 2.1.1 Record Type

A record type is a JSON object of the form
```
{
  id: <number>,
  "kind": "record",
  "fields": [ <field>, <field>, ... ]
}
```
where each of the fields has the form
```
{
  "name": <name>,
  "type": <type>,
}
```
and `<name>` is a string defining the column name and `<type>` is a
recursively encoded type.

#### 2.1.2 Array Type

An array type is defined by a JSON object having the form
```
{
  id: <number>,
  "kind": "array",
  "type": <type>
}
```
where `<type>` is a recursively encoded type.

#### 2.1.3 Set Type

A set type is defined by a JSON object having the form
```
{
  id: <number>,
  "kind": "set",
  "type": <type>
}
```
where `<type>` is a recursively encoded type.

#### 2.1.4 Map Type

A map type is defined by a JSON object of the form
```
{
  id: <number>,
  "kind": "map",
  "key_type": <type>,
  "val_type": <type>
}
```

#### 2.1.5 Union type

A union type is defined by a JSON object having the form
```
{
  id: <number>,
  "kind": "union",
  "types": [ <type>, <type>, ... ]
}
```
where the list of types comprise the types of the union and
and each `<type>`is a recursively encoded type.

#### 2.1.6 Enum Type

An enum type is a JSON object of the form
```
{
  id: <number>,
  "kind": "enum",
  "symbols": [ <string>, <string>, ... ]
}
```

#### 2.1.8 Error Type

An error type is a JSON object of the form
```
{
  id: <number>,
  "kind": "error",
  "type": <type>
}
```

#### 2.1.9 Named Type

A named type is encoded as a binding between a name and a Zed type
and represents a new type so named.  A type definition type has the form
```
{
  id: <number>,
  "kind": "typename",
  "name": <id>,
  "type": <type>,
}
```
where `<id>` is a JSON string representing the newly defined type name
and `<type>` is a recursively encoded type.

### 2.2 Value Encoding

The primitive values comprising an arbitrarily complex Zed data value are encoded
as a JSON array of strings mixed with nested JSON arrays whose structure
conforms to the nested structure of the value's schema as follows:
* each record, array, and set is encoded as a JSON array of its composite values,
* a union is encoded as a string of the form `<tag>:<value>` where `tag`
is an integer string representing the positional index in the union's list of
types that specifies the type of `<value>`, which is a JSON string or array
as described recursively herein,
a map is encoded as a JSON array of two-element arrays of the form
`[ <key>, <value> ]` where `key` and `value` are recursively encoded,
* a type value is encoded [as above](#2-type-encoding),
* each primitive that is not a type value
is encoded as a string conforming to its ZSON representation, as described in the
[corresponding section of the ZSON specification](zson.md#33-primitive-values).

For example, a record with three columns --- a string, an array of integers,
and an array of union of string, and float64 --- might have a value that looks like this:
```
[ "hello, world", ["1","2","3","4"], ["1:foo", "0:10" ] ]
```

## 3. Object Framing

A ZJSON file is composed of ZJSON objects formatted as
[newline delimited JSON (NDJSON)](http://ndjson.org/).
e.g., the [zq](https://github.com/brimdata/zed/tree/main/cmd/zq) CLI command
writes its ZJSON output as lines of NDJSON.

The MIME type `application/x-zjson` is used by the
of the [Zed lake service](../lake/service-api.md) to indicate
ZJSON objects framed as NDJSON.

## 4. Example

> Issue #3340

Here is an example that illustrates values of a repeated type,
nesting, records, array, and union:

```
{s:"hello",r:{a:1 (int32),b:2 (int32)} (=0)} (=1)
{s:"world",r:{a:3,b:4}} (1)
{s:"hello",r:{a:[1 (int32),2 (int32),3 (int32)] (=2)} (=3)} (=4)
{s:"goodnight",r:{x:{u:"foo" (5=((string,int32)))} (=6)} (=7)} (=8)
{s:"gracie",r:{x:{u:12 (int32)}}} (8)
```

This data is represented in ZJSON as follows:

```
{
  "schema": "24",
  "types": [
    {
      "kind": "typedef",
      "name": "24",
      "type": {
        "kind": "record",
        "fields": [
          {
            "name": "s",
            "type": {
              "kind": "primitive",
              "name": "string"
            }
          },
          {
            "name": "r",
            "type": {
              "kind": "record",
              "fields": [
                {
                  "name": "a",
                  "type": {
                    "kind": "primitive",
                    "name": "int32"
                  }
                },
                {
                  "name": "b",
                  "type": {
                    "kind": "primitive",
                    "name": "int32"
                  }
                }
              ]
            }
          }
        ]
      }
    }
  ],
  "values": [
    "hello",
    [
      "1",
      "2"
    ]
  ]
}
{
  "schema": "24",
  "values": [
    "world",
    [
      "3",
      "4"
    ]
  ]
}
{
  "schema": "27",
  "types": [
    {
      "kind": "typedef",
      "name": "27",
      "type": {
        "kind": "record",
        "fields": [
          {
            "name": "s",
            "type": {
              "kind": "primitive",
              "name": "string"
            }
          },
          {
            "name": "r",
            "type": {
              "kind": "record",
              "fields": [
                {
                  "name": "a",
                  "type": {
                    "kind": "array",
                    "type": {
                      "kind": "primitive",
                      "name": "int32"
                    }
                  }
                }
              ]
            }
          }
        ]
      }
    }
  ],
  "values": [
    "hello",
    [
      [
        "1",
        "2",
        "3"
      ]
    ]
  ]
}
{
  "schema": "31",
  "types": [
    {
      "kind": "typedef",
      "name": "31",
      "type": {
        "kind": "record",
        "fields": [
          {
            "name": "s",
            "type": {
              "kind": "primitive",
              "name": "string"
            }
          },
          {
            "name": "r",
            "type": {
              "kind": "record",
              "fields": [
                {
                  "name": "x",
                  "type": {
                    "kind": "record",
                    "fields": [
                      {
                        "name": "u",
                        "type": {
                          "kind": "union",
                          "types": [
                            {
                              "kind": "primitive",
                              "name": "string"
                            },
                            {
                              "kind": "primitive",
                              "name": "int32"
                            }
                          ]
                        }
                      }
                    ]
                  }
                }
              ]
            }
          }
        ]
      }
    }
  ],
  "values": [
    "goodnight",
    [
      [
        [
          "0",
          "foo"
        ]
      ]
    ]
  ]
}
{
  "schema": "31",
  "values": [
    "gracie",
    [
      [
        [
          "1",
          "12"
        ]
      ]
    ]
  ]
}
```
