## Fixes
- An image from a registry path the workspace declares without authentication (for example, a public Harbor project) is pulled anonymously and no longer asks for credentials, even when the same registry also hosts private images.
- Registry credentials are no longer lost when the workspace lists a public path of the same registry after a private one.
