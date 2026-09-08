# Guided Learning

Guided learning adds personal practice to a Wiki knowledge base. It recommends topics, generates source-backed multiple-choice questions and records a reproducible estimate from your submitted answers.

## Availability

This version supports signed-in Web users and Wiki-enabled knowledge bases owned by their active workspace. Shared knowledge bases, shared Agents, API keys, IM and embedded clients are excluded. The workspace's Wiki synthesis model, or its summary model fallback, must be configured for question generation.

Learning starts disabled. Open a Wiki knowledge base and turn on **Personal learning history** in the learning panel. This consent is separate from long-term memory. Existing memory interests and document affinity contribute only when their own controls permit it; learning also works with memory off.

## Practice

1. Open a recommended topic and read its Wiki content and sources.
2. Select **Practice this page**. Generation runs asynchronously; the panel reports pending, ready or failed status.
3. Select one option and submit. The server returns correctness, an explanation and source quotations only after submission.
4. Refresh or reopen the page to retrieve the saved result. Retrying the same submission does not count twice.

Only published entity, concept, synthesis and comparison pages are eligible. A quiz also requires current enabled source-document chunks linked by that page. An index, summary, incomplete source or outdated reference may have no usable quiz evidence.

If the page, source content or generation configuration changes, the old quiz becomes stale and cannot be submitted. Prepare a fresh quiz from the current sources. Ordinary page renames retain the topic identity and history.

## State

**Familiar** means a page was visited or its source documents were used repeatedly. It does not imply a correct answer or mastery.

Practice uses Bayesian Knowledge Tracing with fixed initial mastery 0.20, learning transition 0.15, guess 0.25 and slip 0.10. At least three distinct credited answers and an estimate of 0.85 are required for the mastered label. Review intervals range from one to thirty days. Changed-source history remains visible, with current mastery reset to its prior pending reassessment.

These defaults have not been calibrated on real learners. A displayed probability is an algorithmic estimate, not proof of proficiency. Existing Wiki links identify related topics; they do not establish prerequisite courses.

## Agent

Select the **Guided Learning** built-in Agent, configure its chat model and select a workspace-owned Wiki knowledge base. Its tools can read your overview, recommend topics and prepare a quiz card. The Agent cannot opt in for you, submit answers or change your mastery. Expand the tool result to open a quiz, including after reloading the conversation.

## Privacy

The learning panel offers export and clear controls for the current knowledge base or your entire workspace learning profile. These controls remain available after opt-out. Clearing removes questions, practice history and mastery records in scope; a minimal disabled consent/epoch record remains to prevent a delayed worker from restoring deleted data. A knowledge-base clear also fences queued quizzes for the profile.

Deleting or moving a source document out of scope removes affected quizzes and their saved answers, even when a multi-source Wiki page survives. Export enforces this immediately; background recovery also performs the cleanup. A later assessment using surviving sources is preserved.

Question generation sends bounded Wiki/source text to the configured model provider. It does not send your interests, profile or previous answers. Answer keys are stored server-side and excluded from ordinary model/request diagnostics and Agent transcripts. Exports do not expose unanswered keys.

## Verification

The design, current scope and reproducible synthetic evaluation are in [guided-learning.md](../../docs/guided-learning.md) and [guided-learning-evaluation.md](../../docs/guided-learning-evaluation.md). Offline scores do not establish human learning gains. Source-quote validation and a blinded model check reduce errors but do not prove that every generated question is correct.
