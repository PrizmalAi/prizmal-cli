---
name: attach-screenshots
description: An agent uploads a screenshot to GitHub and embeds it in a pull request body or comment. Use when a PR needs an image, when a changed screen must show in a PR body, or when a local image path renders as nothing on GitHub.
---

# Attach a screenshot to a pull request

A local path in a pull request body or comment renders as nothing. An agent
that has a screenshot on disk uploads the file, then puts the returned URL
in the Markdown.

## Upload the file

GitHub's drag-and-drop posts to an endpoint it does not document. An agent
calls the same endpoint with `curl`.

1. Read the repository id.

   ```bash
   repo_id=$(gh api repos/OWNER/REPO --jq .id)
   ```

2. Upload the PNG. The response is JSON with the attachment URL.

   ```bash
   curl -sS -X POST \
     "https://uploads.github.com/user-attachments/assets?name=shot.png&content_type=image/png&repository_id=$repo_id" \
     -H "Authorization: Bearer $(gh auth token)" \
     -H "Accept: application/json" \
     --data-binary @shot.png
   ```

   ```json
   {"url": "https://github.com/user-attachments/assets/9f1c8a2e-0000-0000-0000-000000000000"}
   ```

3. Put the URL in the body or the comment.

   ```markdown
   ![shot](https://github.com/user-attachments/assets/9f1c8a2e-0000-0000-0000-000000000000)
   ```

`scripts/upload.sh <owner/repo> <image>...` runs the steps and prints the
Markdown for each file.

## Use your user token

The upload works only with a token that belongs to a user, which is what
`gh auth token` returns. The endpoint rejects a GitHub Actions
`GITHUB_TOKEN`.

## Two consequences

- **`repository_id` restricts who can read the asset.** An attachment on a
  private repository resolves only for members who can read it. The URL is
  no public CDN link. A reader outside the repository sees a broken image.
- **A body edit re-runs workflows.** Many repositories run CI on
  `pull_request: edited`, and the re-run can leave a green pull request with
  a red result. To add an image to an existing pull request, post it as a
  comment, which reports `issue_comment` instead. Edit the body only when
  the workflows already expect that.
