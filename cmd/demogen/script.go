package main

import (
	"fmt"
	"path/filepath"
)

// Use case 2: lecture notes in English. Questions are asked in German, so
// the case also covers a question in one language about a source in
// another. Chapter 3 (transactions) is what the exam questions are about;
// two-phase locking (chapter 3) and two-phase commit (chapter 6) are easy
// to confuse.

const (
	scriptFile  = "database-systems.pdf"
	scriptTitle = "Database Systems - Lecture Notes"
)

type chapter struct {
	title    string
	sections []section
}

type section struct {
	title string
	paras []string
}

var scriptChapters = []chapter{
	{"Introduction", []section{
		{"Database Systems and Their Tasks", []string{
			"A database system consists of a database, the collection of stored data, and a database management system (DBMS), the software that manages it. Applications do not access files directly; they send requests to the DBMS, which takes care of storage, access paths, concurrency and recovery. This separation is what allows many users and programs to share the same data safely.",
			"Typical tasks of a DBMS are the integration of data into one consistent collection, operations to query and modify data, a catalog that describes the stored data, user views, integrity constraints, access control, transactions and recovery after failures. Together these tasks distinguish a database system from a set of files that each application manages on its own.",
		}},
		{"The Three-Schema Architecture", []string{
			"The ANSI/SPARC architecture describes a database on three levels. The internal schema defines how data is stored physically, for example in which files and with which indexes. The conceptual schema describes the logical structure of the whole database: entities, attributes and relationships. External schemas define the views that individual user groups or applications see.",
			"Mappings connect the levels. A query against an external view is translated into a query against the conceptual schema and then into operations on the internal level. Because each level only depends on the mapping to the next one, changes on one level can often be hidden from the levels above.",
		}},
		{"Data Independence", []string{
			"Physical data independence means that the internal schema can change, for example by adding an index or moving data to another disk, without changing the conceptual schema or the applications. Logical data independence means that the conceptual schema can be extended, for example by a new attribute, without changing external views that do not use it.",
			"Data independence is the main reason why database applications survive decades of changes in hardware and storage technology. It is also why query languages are declarative: an application states which data it needs, not how the DBMS should find it.",
		}},
	}},
	{"The Relational Model and SQL", []section{
		{"Relations and Keys", []string{
			"In the relational model, data is stored in relations, which are sets of tuples over a fixed list of attributes. Each attribute has a domain of atomic values. A relation schema consists of the relation name and its attributes; the relation itself is the current set of tuples.",
			"A superkey is a set of attributes whose values identify each tuple uniquely. A candidate key is a minimal superkey, and one candidate key is chosen as the primary key. A foreign key is a set of attributes that refers to the primary key of another relation; referential integrity requires that every foreign key value exists in the referenced relation.",
		}},
		{"Relational Algebra", []string{
			"Relational algebra provides operators that take relations as input and produce relations as output. Selection chooses tuples that satisfy a condition, projection keeps a subset of attributes, and the Cartesian product combines every tuple of one relation with every tuple of another. Union and difference work on relations with the same schema, and renaming changes attribute names.",
			"The join combines tuples of two relations that agree on given attributes; it can be expressed as a product followed by a selection. Because every operator returns a relation, expressions can be nested arbitrarily, which is the basis for query optimization: equivalent expressions can be evaluated at very different costs.",
		}},
		{"Queries in SQL", []string{
			"SQL is the standard query language of relational systems. A query has the basic form SELECT attributes FROM relations WHERE condition. Joins can be written as conditions in the WHERE clause or with the JOIN keyword. GROUP BY partitions the result into groups, and aggregate functions such as COUNT, SUM and AVG compute one value per group.",
			"Subqueries may appear in the WHERE clause, for example with IN or EXISTS. ORDER BY sorts the result. Unlike relational algebra, SQL works with multisets: duplicates are kept unless DISTINCT is specified.",
		}},
		{"Normal Forms", []string{
			"Normalization avoids redundancy and the update anomalies it causes. A relation is in first normal form if all attribute values are atomic. It is in second normal form if, in addition, no non-key attribute depends on only part of a candidate key. Third normal form also excludes transitive dependencies of non-key attributes on a key.",
			"Boyce-Codd normal form requires that the left side of every non-trivial functional dependency is a superkey. Decomposition into third normal form is always possible without losing information or dependencies; for Boyce-Codd normal form, preserving all dependencies is not always possible.",
		}},
	}},
	{"Transactions", []section{
		{"The ACID Properties", []string{
			"A transaction is a sequence of operations that the DBMS treats as one logical unit, for example transferring money from one account to another. Transactions guarantee four properties, abbreviated as ACID. Atomicity means that either all operations of a transaction take effect or none does. Consistency means that a transaction transforms a consistent database state into another consistent state.",
			"Isolation means that concurrent transactions do not see each other's intermediate results; the effect is as if they ran one after another. Durability means that the effects of a committed transaction survive later failures, including crashes of the system. The concurrency control component ensures isolation; the recovery component ensures atomicity and durability.",
		}},
		{"Serializability", []string{
			"A schedule is an interleaving of the operations of several transactions. A schedule is serial if the transactions run one after another. A schedule is serializable if it has the same effect as some serial schedule. Two operations conflict if they belong to different transactions, access the same data item and at least one of them is a write.",
			"Conflict serializability can be tested with the conflict graph (also called precedence graph): it has a node for each transaction and an edge from T1 to T2 if an operation of T1 conflicts with a later operation of T2. A schedule is conflict serializable if and only if its conflict graph is acyclic; a topological order of the graph gives an equivalent serial schedule.",
		}},
		{"Two-Phase Locking", []string{
			"Most systems ensure serializability with locks. Before reading a data item, a transaction acquires a shared lock; before writing it, an exclusive lock. Shared locks are compatible with each other, exclusive locks are compatible with no other lock. A transaction that requests an incompatible lock has to wait.",
			"The two-phase locking protocol (2PL) requires that a transaction acquires all its locks before it releases any lock. Each transaction therefore has a growing phase and a shrinking phase. Every schedule produced by 2PL is conflict serializable. Strict two-phase locking holds all exclusive locks until the transaction commits or aborts, which additionally avoids cascading aborts.",
		}},
		{"Deadlocks", []string{
			"Locking can lead to deadlocks: transaction T1 waits for a lock held by T2, while T2 waits for a lock held by T1. Neither transaction can continue. Deadlocks are detected with the wait-for graph, which has an edge from Ti to Tj if Ti waits for a lock held by Tj. A cycle in the wait-for graph is a deadlock.",
			"The DBMS resolves a deadlock by aborting one transaction of the cycle, the victim, and restarting it later. Alternatively, deadlocks can be prevented, for example with timestamps: in the wait-die scheme, an older transaction may wait for a younger one, while a younger transaction that requests a lock held by an older one is aborted.",
		}},
		{"Isolation Levels", []string{
			"Full serializability costs concurrency, so SQL defines weaker isolation levels. Read uncommitted allows dirty reads of data written by uncommitted transactions. Read committed prevents dirty reads but allows non-repeatable reads. Repeatable read guarantees that data read once does not change, but phantoms may appear: new rows that match a query condition.",
			"Serializable, the strongest level, prevents all of these anomalies. Many systems implement isolation with multiversion concurrency control, where readers see a snapshot of the database and do not block writers.",
		}},
		{"Logging and Recovery", []string{
			"To guarantee atomicity and durability, the DBMS writes a log of all changes. Each log record contains the transaction, the changed data item, its old value (before image) and its new value (after image). The write-ahead logging rule (WAL) requires that a log record is written to stable storage before the changed data page, and that all log records of a transaction are written before it commits.",
			"After a crash, recovery uses the log in two directions. Redo repeats the changes of committed transactions whose pages had not yet been written; undo reverses the changes of transactions that had not committed. Checkpoints limit how much of the log has to be read. The ARIES algorithm combines these steps in an analysis, a redo and an undo phase.",
		}},
	}},
	{"Index Structures", []section{
		{"B+-Trees", []string{
			"The B+-tree is the standard index structure of relational systems. It is a balanced search tree in which all data entries are stored in the leaves, which are linked for range scans. Inner nodes only contain separator keys and pointers. Each node except the root is at least half full, so the height of the tree grows only logarithmically with the number of entries.",
			"Insertion splits a full node and propagates a separator key to the parent; deletion merges or redistributes nodes that become less than half full. A lookup reads one node per level, typically three or four pages even for very large tables.",
		}},
		{"Hashing", []string{
			"Hash indexes compute the bucket of an entry from a hash function of its key. Equality lookups need only one or two page accesses, but range queries are not supported. Static hashing degrades when buckets overflow; dynamic schemes such as extendible hashing and linear hashing grow the number of buckets with the data.",
		}},
		{"Bitmap Indexes", []string{
			"A bitmap index stores, for each value of an attribute, a bit vector with one bit per row. Bitmap indexes are compact for attributes with few distinct values and allow fast combination of conditions with bitwise AND and OR, which makes them popular in data warehouses.",
		}},
	}},
	{"Query Processing", []section{
		{"Query Optimization", []string{
			"The query optimizer translates a declarative query into an efficient execution plan. Logical optimization applies algebraic rules, for example pushing selections down towards the base relations so that intermediate results stay small. Physical optimization chooses algorithms and access paths for each operator.",
			"A cost model estimates the cost of a plan from statistics such as table sizes, the number of distinct values and histograms. For joins of many relations, the optimizer searches the space of join orders, typically with dynamic programming.",
		}},
		{"Join Algorithms", []string{
			"The nested-loop join compares every tuple of the outer relation with every tuple of the inner relation; the block nested-loop variant reads the inner relation once per block of the outer one. An index nested-loop join uses an index on the join attribute of the inner relation.",
			"The sort-merge join sorts both inputs on the join attribute and merges them. The hash join builds a hash table on the smaller input and probes it with the tuples of the larger one; it is usually the fastest algorithm for equality joins of large unsorted inputs.",
		}},
	}},
	{"Distributed Databases", []section{
		{"Fragmentation and Replication", []string{
			"A distributed database stores data at several sites. Horizontal fragmentation splits a relation into subsets of rows, for example by region; vertical fragmentation splits it into subsets of columns. Replication keeps copies of fragments at several sites to improve availability and read performance, at the cost of keeping the copies consistent.",
		}},
		{"Distributed Commit", []string{
			"A transaction that changes data at several sites must commit everywhere or nowhere. The two-phase commit protocol (2PC) achieves this with a coordinator. In the voting phase, the coordinator asks all participants whether they can commit; each participant writes its changes to its log and answers yes or no. In the decision phase, the coordinator sends commit if all participants voted yes and abort otherwise.",
			"Two-phase commit blocks if the coordinator fails after the voting phase: participants that voted yes cannot decide on their own. Three-phase commit and consensus protocols such as Paxos reduce this problem.",
		}},
		{"The CAP Theorem", []string{
			"The CAP theorem states that a distributed system cannot guarantee consistency, availability and partition tolerance at the same time. Since network partitions cannot be ruled out, systems choose between consistency and availability during a partition; many NoSQL stores choose availability and offer eventual consistency.",
		}},
	}},
}

// scriptLayout lays out the notes. toc holds the page of each section
// ("3.3" → 7); pass nil to measure pages first.
func scriptLayout(toc map[string]int) (*layout, map[string]int) {
	l := newLayout(scriptTitle)
	pages := map[string]int{}
	l.gap(60)
	l.line(marginL, 22, true, "Database Systems", 12)
	l.line(marginL, 14, false, "Lecture Notes, Winter Term 2025/26", 10)
	l.line(marginL, 11, false, "Hochschule Musterstadt, Department of Computer Science", 8)

	l.pages++
	l.y = marginTop
	l.line(marginL, 16, true, "Contents", 10)
	for ci, c := range scriptChapters {
		entry := func(num, title string, size float64, bold bool) {
			l.at(marginL, size, bold, num+" "+title)
			if p, ok := toc[num]; ok {
				l.at(180, size, bold, fmt.Sprint(p))
			}
			l.gap(5.5)
		}
		entry(fmt.Sprint(ci+1), c.title, 11, true)
		for si, s := range c.sections {
			entry(fmt.Sprintf("%d.%d", ci+1, si+1), s.title, 10, false)
		}
	}

	for ci, c := range scriptChapters {
		l.pages++
		l.y = marginTop
		num := fmt.Sprint(ci + 1)
		pages[num] = l.pages
		l.line(marginL, 18, true, fmt.Sprintf("%s %s", num, c.title), 12)
		for si, s := range c.sections {
			l.need(30)
			snum := fmt.Sprintf("%d.%d", ci+1, si+1)
			pages[snum] = l.pages
			l.line(marginL, 13, true, snum+" "+s.title, 8)
			for _, p := range s.paras {
				l.para(10.5, false, p)
			}
			l.gap(4)
		}
	}
	return l, pages
}

// scriptPages lays out the notes twice so that the table of contents shows
// the pages the sections end up on.
func scriptPages() (*layout, map[string]int) {
	_, pages := scriptLayout(nil)
	return scriptLayout(pages)
}

func writeScript(dir string) error {
	l, _ := scriptPages()
	return l.writePDF(filepath.Join(dir, scriptFile), scriptTitle)
}

// scriptSectionPage is the page a section of the notes starts on.
func scriptSectionPage(num string) int {
	_, pages := scriptPages()
	return pages[num]
}
