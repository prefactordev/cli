package output

import "testing"

// Expected values come from Bun: JSON.stringify(JSON.parse(input), null, 2).
var stringifyCases = []struct{ input, want string }{
	{"{\"a\":1.0,\"b\":2.50,\"c\":-0,\"d\":0.0,\"e\":1e2,\"f\":1E-7,\"g\":0.000001,\"h\":1e21,\"i\":123456789012345678901234,\"j\":9007199254740993,\"k\":-1.5e-10,\"l\":1e300,\"m\":5e-324,\"n\":0.1,\"o\":100}", "{\n  \"a\": 1,\n  \"b\": 2.5,\n  \"c\": 0,\n  \"d\": 0,\n  \"e\": 100,\n  \"f\": 1e-7,\n  \"g\": 0.000001,\n  \"h\": 1e+21,\n  \"i\": 1.2345678901234569e+23,\n  \"j\": 9007199254740992,\n  \"k\": -1.5e-10,\n  \"l\": 1e+300,\n  \"m\": 5e-324,\n  \"n\": 0.1,\n  \"o\": 100\n}"},
	{"{\"s\":\"caf\\u00e9 \\u2028 \\u2029 \\/ \\\" \\\\ \\b\\f\\n\\r\\t \\u0001 \\u001f \\u007f <>&'\"}", "{\n  \"s\": \"caf\U000000e9 \U00002028 \U00002029 / \\\" \\\\ \\b\\f\\n\\r\\t \\u0001 \\u001f \x7f <>&'\"\n}"},
	{"{\"z\":1,\"a\":2,\"z\":3}", "{\n  \"z\": 3,\n  \"a\": 2\n}"},
	{"{\"empty_obj\":{},\"empty_arr\":[],\"nested\":[[],[{}],{\"x\":[1,[2,[3]]]}]}", "{\n  \"empty_obj\": {},\n  \"empty_arr\": [],\n  \"nested\": [\n    [],\n    [\n      {}\n    ],\n    {\n      \"x\": [\n        1,\n        [\n          2,\n          [\n            3\n          ]\n        ]\n      ]\n    }\n  ]\n}"},
	{"[1,\"two\",true,false,null,{\"k\":\"v\"}]", "[\n  1,\n  \"two\",\n  true,\n  false,\n  null,\n  {\n    \"k\": \"v\"\n  }\n]"},
	{"\"just a string\"", "\"just a string\""},
	{"42", "42"},
	{"null", "null"},
	{"{\"big\":1e400}", "{\n  \"big\": null\n}"},
	{"{\"emoji\":\"\\ud83d\\ude00\"}", "{\n  \"emoji\": \"\U0001f600\"\n}"},
}

func TestFormatMatchesJSONStringify(t *testing.T) {
	for _, tc := range stringifyCases {
		got, err := Format([]byte(tc.input))
		if err != nil {
			t.Fatalf("Format(%q): %v", tc.input, err)
		}
		if string(got) != tc.want {
			t.Errorf("Format(%q)\n got: %q\nwant: %q", tc.input, got, tc.want)
		}
	}
}

func TestFormatEmptyAndInvalidBodies(t *testing.T) {
	if got, _ := Format(nil); string(got) != "null" {
		t.Errorf("empty body = %q, want null", got)
	}
	// Only an empty body is null; whitespace is a body that is not JSON.
	if got, _ := Format([]byte("  ")); string(got) != `"  "` {
		t.Errorf("whitespace body = %q", got)
	}
	if got, _ := Format([]byte("not json")); string(got) != `"not json"` {
		t.Errorf("invalid body = %q", got)
	}
}
