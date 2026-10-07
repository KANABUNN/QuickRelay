import SwiftData

@MainActor
enum PersistenceController {
    static func makeContainer(inMemory: Bool = false) throws -> ModelContainer {
        let schema = Schema(PersistenceSchema.models)
        let configuration = ModelConfiguration(
            "QuakeRelay",
            schema: schema,
            isStoredInMemoryOnly: inMemory
        )
        let container = try ModelContainer(for: schema, configurations: [configuration])
        container.mainContext.autosaveEnabled = false
        return container
    }
}
