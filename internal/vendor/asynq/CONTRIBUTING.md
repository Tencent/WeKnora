# Contributing

Thanks for your interest in contributing to Asynq!
We are open to, and grateful for, any contributions made by the community.

## Reporting Bugs

Have a look at our [issue tracker](https://github.com/hibiken/asynq/issues). If you can't find an issue (open or closed)
describing your problem (or a very similar one) there, please open a new issue with
the following details:

- Which versions of Go and Redis are you using?
- What are you trying to accomplish?
- What is the full error you are seeing?
- How can we reproduce this?
  - Please quote as much of your code as needed to reproduce (best link to a
    public repository or Gist)

## Getting Help

We run a [Gitter
channel](https://gitter.im/go-asynq/community) where you can ask questions and
get help. Feel free to ask there before opening a GitHub issue.

## Submitting Feature Requests

If you can't find an issue (open or closed) describing your idea on our [issue
tracker](https://github.com/hibiken/asynq/issues), open an issue. Adding answers to the following
questions in your description is +1:

- What do you want to do, and how do you expect Asynq to support you with that?
- How might this be added to Asynq?
- What are possible alternatives?
- Are there any disadvantages?

Thank you! We'll try to respond as quickly as possible.

## Contributing Code

1. Fork this repo
2. Download your fork `git clone git@github.com:your-username/asynq.git && cd asynq`
3. Create your branch `git checkout -b your-branch-name`
4. Make and commit your changes
5. Push the branch `git push origin your-branch-name`
6. Create a new pull request

Please try to keep your pull request focused in scope and avoid including unrelated commits.
Please run tests against redis cluster locally with `--redis_cluster` flag to ensure that code works for Redis cluster. TODO: Run tests using Redis cluster on CI.

After you have submitted your pull request, we'll try to get back to you as soon as possible. We may suggest some changes or improvements.

Thank you for contributing!
