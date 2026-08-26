# Deep Module Reference

This file defines the categories of dependencies and how to handle them when designing deep modules.

## Dependency Categories

### 1. **Direct** 
A direct dependency is a module that the new module calls and whose behavior is controlled by the new module. 
Example: A function that writes to a file, the new module controls what file is written to. 
This dependency is usually a simple type and doesn't need to be abstracted.

### 2. **Indirect** 
An indirect dependency is a module that gets called, but whose behavior is NOT controlled by the new module. 
Example: A function that makes an HTTP call, the new module doesn't control which server or URL the call goes to.
This dependency needs to be abstracted by interface or injection to make it testable.

### 3. **Cross-boundary** 
A cross-boundary dependency spans two separate systems - it usually involves I/O, networking, databases, external services.
Example: Reading from disk, connecting to a database, sending emails.
This dependency can be abstracted by either:
- A port & adapter pattern
- An interface

### 4. **Internal** 
An internal dependency is a module that the new module calls, but that isn't controlled (in the sense of the new module being the owner of its interface).
Example: A function that uses a standard library or an external library like "strings".
This dependency is usually a type that can be abstracted if needed.
This dependency is often not abstracted and is just used directly.

## Deep Module Design Principles

- **Small interfaces, large implementations**: The module's surface is small but it hides complexities
- **Testability**: Code should be testable at the boundary
- **Separation of concerns**: Clear separation between the module's interface and implementation
- **Minimal coupling**: Only essential dependencies should be included

## Example: Dependency Category Recognition

Looking at the typical patterns for a hypothetical logging module:
```
function log(message) {
  // This depends on:
  // - console (internal - standard nodejs)
  // - process (internal - standard nodejs)  
  // - file system (cross-boundary - I/O)
  // - network (cross-boundary - I/O)
  
  // Implementation: 
  // 1. Write to console (internal)
  // 2. Write to file (cross-boundary)
  // 3. Send to server (cross-boundary)
}
```

This would be categorized as:
* console: internal
* process: internal 
* file system: cross-boundary
* network: cross-boundary

The interface would be small - one function signature: `log(message)` 
The implementation would hide the complex cross-boundary dependencies