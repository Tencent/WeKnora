# Triage Labels

The skills speak in terms of five canonical triage roles. This file maps those roles to the actual labels in this Multica workspace.

| Label in mattpocock/skills | Label in our tracker | Meaning                                  |
| -------------------------- | -------------------- | ---------------------------------------- |
| `needs-triage`             | `needs-triage`       | Maintainer needs to evaluate this issue  |
| `needs-info`               | `needs-info`         | Waiting on reporter for more information |
| `ready-for-agent`          | `ready-for-agent`    | Fully specified, ready for an AFK agent  |
| `ready-for-human`          | `ready-for-human`    | Requires human implementation            |
| `wontfix`                  | `wontfix`            | Will not be actioned                     |

When a skill mentions a role (e.g. "apply the AFK-ready triage label"), use the corresponding label string from this table, then resolve it to a UUID before touching the issue. Edit the right-hand column to match whatever vocabulary this workspace actually uses.

## Creating the labels

`--color` is required; without it the command fails. Run once per workspace.

```bash
multica label create --name needs-triage     --color "#d73a4a" --description "Maintainer needs to evaluate this issue"
multica label create --name needs-info       --color "#fbca04" --description "Waiting on reporter for more information"
multica label create --name ready-for-agent  --color "#0e8a16" --description "Fully specified, ready for an AFK agent"
multica label create --name ready-for-human  --color "#1d76db" --description "Requires human implementation"
multica label create --name wontfix          --color "#6a737d" --description "Will not be actioned"
```

Re-running with an existing name is not a no-op in every case: check `multica label list --output json` first and reuse the UUID if the name is already there.

## Applying a label to an issue

Two steps, always: `issue create` has **no** `--label` flag, and `issue label add` takes **UUIDs, not names**.

PowerShell:

```powershell
$labels = (multica label list --output json | ConvertFrom-Json)
$id = ($labels | Where-Object { $_.name -eq 'ready-for-agent' }).id
multica issue label add <issue-UUID> $id
```

POSIX shell:

```bash
id=$(multica label list --output json | jq -r '.[] | select(.name=="ready-for-agent") | .id')
multica issue label add <issue-UUID> "$id"
```

## One status label plus one category label

An issue carries **one** status label from the five above plus, at most, **one** category label (a `wayfinder:<type>` type, an area, a component). Never two status labels: `needs-triage` and `ready-for-agent` cannot both be true, and a reader that sees both has no way to pick.

**Replacing a label happens in the same round: remove the old one first, then add the new one.** Add-then-remove leaves a window where the issue carries two status labels, and any frontier query that runs inside that window gets the wrong answer.

```bash
multica issue label remove <issue-UUID> <old-label-UUID>
multica issue label add    <issue-UUID> <new-label-UUID>
```

Other agents cannot be relied on to clean this up: they read labels, they do not audit them.
