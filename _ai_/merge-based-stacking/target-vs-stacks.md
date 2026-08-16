# Pulumi `--target` vs Separate Stacks for Zero-Day ECR Setup

Research findings for bootstrapping ECR repositories before deploying the full infrastructure stack.

## Current Setup

The `ecrServices.ts` module (commit ee88299) already includes a phased deployment example using `--target`:

```typescript
// Phase 1 - Deploy ECR infrastructure:
pulumi up --target 'urn:pulumi:*::*::aws:ecr/*' --target 'urn:pulumi:*::*::aws:kms/*'
```

## How `pulumi up --target` Works

### Basic Usage

- Deploys only specific resources by their URN (Uniform Resource Name)
- URN format: `urn:pulumi:<Stack>::<Project>::<Type>::<Name>`
- Find URNs via `pulumi stack --show-urns` or the Pulumi Console
- Supports wildcards: `*` (single level) and `**` (multiple levels)

### Key Characteristics

- Only works with **already created** resources (updates existing state)
- For **first-time deployment**, targets all resources matching the pattern
- Respects dependencies: if you target Resource A that depends on Resource B, Pulumi will deploy B first
- Multiple targets: `--target urn1 --target urn2`

### Zero-Day Use Case

```bash
# Phase 1: Deploy only ECR + KMS infrastructure
pulumi up --target 'urn:pulumi:*::*::aws:ecr/*' --target 'urn:pulumi:*::*::aws:kms/*'

# Phase 2: Build and push Docker images (outside Pulumi)
docker build -t <ecr-url>/coverbase-api:latest ./api
docker push <ecr-url>/coverbase-api:latest
# ... repeat for other services

# Phase 3: Deploy full stack (ECS services, RDS, etc.)
pulumi up
```

### Advantages

- Single stack - all resources in one state file
- Simple to understand - linear deployment phases
- Good for one-time bootstrap scenarios
- No need to manage stack references

### Disadvantages

- **Must remember to use `--target` correctly** - easy to forget on subsequent deployments
- **Not automatic** - requires manual intervention for phased deployment
- **Brittle** - if URN patterns change (resource type changes), commands break
- **Limited reusability** - targeting logic exists outside of code
- **State complexity** - all resources in one state even if deployed at different times
- **Risky for teams** - developers might accidentally deploy everything with `pulumi up`

## Alternative Solutions

### 1. Separate Stacks (Recommended for Production)

Create separate Pulumi projects/stacks with explicit dependencies:

```
irm-ecr/          # Bootstrap stack - ECR repositories only
  - Pulumi.yaml
  - index.ts      # Creates ECR repos, exports URLs

irm-infra/        # Main infrastructure stack
  - Pulumi.yaml
  - index.ts      # References ECR stack outputs
```

#### Implementation

```typescript
// In irm-ecr/index.ts
export const ecrUrls = {
  api: apiRepo.repositoryUrl,
  dashboard: dashboardRepo.repositoryUrl,
  // ...
};

// In irm-infra/index.ts
import * as pulumi from '@pulumi/pulumi';

const ecrStack = new pulumi.StackReference('irm-ecr-prod');
const apiRepoUrl = ecrStack.getOutput('ecrUrls').api;

// Use apiRepoUrl in ECS task definitions
```

#### Advantages

- **Automatic dependency management** via stack references
- **Clear separation of concerns** - bootstrap vs. runtime infrastructure
- **Safer** - can't accidentally deploy ECR changes when deploying main infra
- **Independent lifecycles** - update ECR policies without touching ECS
- **Team-friendly** - different RBAC for bootstrap vs. app infrastructure
- **Reusable** - ECR stack can be shared across multiple app stacks

#### Disadvantages

- More complex setup - two separate Pulumi projects
- Must manage stack references
- Harder to visualize full dependency graph
- Need to deploy stacks in correct order

### 2. Component Resources with Explicit Ordering

Keep single stack but use Pulumi's `dependsOn` to control ordering:

```typescript
// Create ECR first
const ecrServices = createEcrServices({ sharedKmsKey });

// Explicitly wait for ECR before creating ECS services
const apiService = new awsx.ecs.FargateService('api', {
  taskDefinitionArgs: {
    container: {
      image: ecrServices.api.repositoryUrl.apply(url => `${url}:latest`),
    },
  },
}, { dependsOn: [ecrServices.api.repository] });
```

#### Advantages

- Single stack - simpler state management
- Dependencies encoded in code
- Standard `pulumi up` works correctly

#### Disadvantages

- **Doesn't solve the zero-day problem** - you still need images in ECR before ECS can start
- Pulumi will try to deploy ECS immediately, which will fail if images don't exist
- Not suitable for your use case

### 3. External Bootstrap Script

Use a script to orchestrate the deployment:

```bash
#!/bin/bash
# bootstrap.sh

echo "Phase 1: Deploying ECR infrastructure..."
pulumi up --target 'urn:pulumi:*::*::aws:ecr/*' --target 'urn:pulumi:*::*::aws:kms/*' --yes

echo "Phase 2: Building and pushing images..."
export ECR_URL=$(pulumi stack output ecrUrls --json | jq -r '.api')
docker build -t $ECR_URL:latest ./api && docker push $ECR_URL:latest
# ... other services

echo "Phase 3: Deploying full stack..."
pulumi up --yes
```

#### Advantages

- Automated workflow
- Keeps single stack
- Documents the process

#### Disadvantages

- Shell script maintenance
- Not idempotent - have to handle "already deployed" cases
- Extra tooling dependency

### 4. Pulumi Automation API

Write TypeScript/Python code to orchestrate the deployment programmatically:

```typescript
import * as automation from '@pulumi/pulumi/automation';

const stack = await automation.LocalWorkspace.createOrSelectStack({
  stackName: 'prod',
  projectName: 'irm',
});

// Phase 1: Deploy ECR
await stack.up({
  target: ['urn:pulumi:*::*::aws:ecr/*', 'urn:pulumi:*::*::aws:kms/*'],
});

// Phase 2: Build images (call external scripts)
// ...

// Phase 3: Deploy everything
await stack.up();
```

#### Advantages

- Fully automated and programmable
- Type-safe deployment logic
- Can integrate with CI/CD easily
- Error handling in code

#### Disadvantages

- More complex than shell script
- Requires Automation API knowledge
- Additional code to maintain

## Recommendation

### For Now (Simple Bootstrap)

Use the `--target` approach documented in `ecrServices.ts`:

```bash
pulumi up --target 'urn:pulumi:*::*::aws:ecr/*' --target 'urn:pulumi:*::*::aws:kms/*'
```

This is fine for one-time zero-day setup and matches what's already documented in your code.

### For Production (Long-term)

Migrate to **separate stacks** if:

- Multiple teams manage different parts of infrastructure
- ECR repositories need independent lifecycle management
- You want to prevent accidental changes to bootstrap infrastructure
- You need different RBAC permissions for ECR vs. application infrastructure

The separate stacks approach aligns better with the "infrastructure as code" philosophy where dependencies are explicit and automated.

## Sources

- [Pulumi up command documentation](https://www.pulumi.com/docs/iac/cli/commands/pulumi_up/)
- [Resource Names and URNs](https://www.pulumi.com/docs/iac/concepts/resources/names/)
- [Pulumi --target wildcard support](https://github.com/pulumi/pulumi/issues/5870)
- [Selectively Replacing Resources with Pulumi](https://blog.scottlowe.org/2024/01/03/selectively-replacing-resources-with-pulumi/)
- [Organizing Projects & Stacks](https://www.pulumi.com/docs/iac/guides/basics/organizing-projects-stacks/)
- [IaC Best Practices: Stack References](https://www.pulumi.com/blog/iac-best-practices-applying-stack-references/)
- [Dependent Stack Updates with Pulumi Deployments](https://www.pulumi.com/blog/dependent-stack-updates/)
- [Managing Pulumi Micro-Stack Dependencies](https://ersenal.com/posts/pulumi-micro-stack/)
- [Exploring circular dependencies](https://www.pulumi.com/blog/exploring-circular-dependencies/)
- [Using AWS ECR with Pulumi](https://www.pulumi.com/docs/iac/clouds/aws/guides/ecr/)
