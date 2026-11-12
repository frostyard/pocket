# After you create the repository on GitHub

The admin steps that a template does not copy, for frostyard/pocket, once the
repository exists on GitHub.

[Back to the README](../README.md).

1. Create the 5 labels:

   ```sh
   gh label create risk:high --repo frostyard/pocket --color B60205 --description "Touches a path in .github/risk-paths.json"
   gh label create level:L1 --repo frostyard/pocket --color B60205 --description "Read every line"
   gh label create level:L2 --repo frostyard/pocket --color D93F0B --description "Read the diff"
   gh label create level:L3 --repo frostyard/pocket --color FBCA04 --description "Skim"
   gh label create level:L4 --repo frostyard/pocket --color 0E8A16 --description "Title and CI"
   ```

   Labels are not copied from a template. Without `risk:high`, the PR triage job
   fails. Without a level label, it runs, writes a warning to the job summary and
   adds no level label. As an organization admin you can instead run core's
   script from a frostyard/core checkout; it creates the 4 level labels and
   never changes an existing label:

   ```sh
   scripts/apply-triage-settings.sh frostyard/pocket --apply
   ```

2. Add a ruleset on the default branch: it requires the `verify` check and a
   pull request for every change, and it blocks deletion and force-push.
   Repository admins can bypass it. Run it as a repository admin:

   ```sh
   gh api -X POST repos/frostyard/pocket/rulesets --input - <<'EOF'
   {"name":"main: require verify","target":"branch","enforcement":"active",
    "conditions":{"ref_name":{"include":["~DEFAULT_BRANCH"],"exclude":[]}},
    "bypass_actors":[{"actor_id":5,"actor_type":"RepositoryRole","bypass_mode":"always"}],
    "rules":[{"type":"deletion"},{"type":"non_fast_forward"},
     {"type":"pull_request","parameters":{"required_approving_review_count":0,"dismiss_stale_reviews_on_push":false,
      "require_code_owner_review":false,"require_last_push_approval":false,"required_review_thread_resolution":false}},
     {"type":"required_status_checks","parameters":{"strict_required_status_checks_policy":false,
      "do_not_enforce_on_create":false,"required_status_checks":[{"context":"verify","integration_id":15368}]}}]}
   EOF
   ```

   Repository rulesets are not copied from a template. `15368` is the GitHub
   Actions app; `actor_id` 5 is the repository admin role.

3. Enable private vulnerability reporting. Repository settings are not copied
   from a template, so no repository made from this one has it. Run it as a
   repository admin:

   ```sh
   gh api -X PUT repos/frostyard/pocket/private-vulnerability-reporting
   ```

4. Add your repository's high-risk paths to `.github/risk-paths.json`. The list
   is schema version 2: give every glob a level from `L1` to `L4` and a `why`
   that names its category, and set `default_level` (`L3` is the usual choice).
   Only L1 and L2 add `risk:high`. Use the categories, and the grading rule from
   version 1 to version 2, of the
   [core risk-path specification](https://github.com/frostyard/core/blob/main/docs/specs/risk-paths.md).
