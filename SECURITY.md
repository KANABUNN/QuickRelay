# Security

Do not include credentials, pairing codes, device tokens, deployment logs, databases, or signing files in issues or pull requests. Keep production configuration outside Git.

Pull request checks use isolated synthetic data and no signing credentials. Internal TestFlight uploads use a protected environment, reviewed release tags, and owner approval. External contributions must not run against the owner's VPS.

The public repository is source code, not a public notification service. Simulator checks do not certify real APNs delivery or production operation.
