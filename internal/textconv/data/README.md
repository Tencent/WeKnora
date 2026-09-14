# Traditional-to-simplified dictionary data

`TSPhrases.txt` and `TSCharacters.txt` are unmodified copies from
[longbridgeapp/opencc v0.3.13](https://github.com/longbridgeapp/opencc/tree/v0.3.13/dictionary),
which distributes OpenCC dictionary data under Apache-2.0. Attribution belongs
to the OpenCC and longbridge/opencc contributors. The full license is retained
in [`licenses/OpenCC-Apache-2.0.txt`](../../../licenses/OpenCC-Apache-2.0.txt).

Only these two text dictionaries are retained. The Go converter, `liuzl/da`,
and the GPL-licensed `cedar-go` implementation are not vendored or imported.
The lookup implementation in the parent directory uses Go standard-library
maps and preserves the previous t2s dictionary order and first-choice values.

SHA-256:

| File | Digest |
| --- | --- |
| TSCharacters.txt | 6b5a0a799bea2bb22c001f635eaa3fc2904310f0c08addbff275477a80ecf09a |
| TSPhrases.txt | b2ef895dd4953b4bb77fc8ef8d26a2a9ca6d43a760ed9a1d767672cfafa6324f |

Dictionary updates can change FAQ normalization and persisted content hashes.
Review them separately from converter changes; the historical corpus test pins
the behavior of the original v0.3.13 converter on 13,177 inputs.
